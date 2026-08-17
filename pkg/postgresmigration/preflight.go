// Package postgresmigration contains the side-effect-free checks that gate the
// future one-shot primary database copier.
//
// This package deliberately does not open a database or execute SQL.  A later
// slice may build SchemaSnapshot values from an explicitly approved snapshot;
// keeping this boundary pure makes it impossible for a preflight to mutate a
// source or target accidentally.
package postgresmigration

import (
	"sort"
	"strings"
)

const (
	AllowlistSQLite34 = "sqlite-34-pre-enterprise"

	SourceSQLite   DatabaseType = "sqlite"
	SourceMySQL    DatabaseType = "mysql"
	TargetPostgres DatabaseType = "postgres"
)

type DatabaseType string

type LogScope string

const (
	LogPrimary          LogScope = "primary"
	LogSeparateRetain   LogScope = "separate-retain"
	LogSeparateMigrate  LogScope = "separate-migrate"
	LogClickHouseRetain LogScope = "clickhouse-retain"
)

// Manifest contains only non-secret preflight inputs. Connection information
// is intentionally not represented here and must never be written to a report.
type Manifest struct {
	CandidateSHA       string
	SourceType         DatabaseType
	TargetType         DatabaseType
	LogScope           LogScope
	AllowlistVersion   string
	BatchSize          int
	TargetIdentityHash string
	SnapshotProven     bool
}

type TableSnapshot struct {
	Columns  []ColumnMetadata
	RowCount int64
}

// ColumnSpec is the compile-time contract for one source column. SQLite
// metadata is input to comparison only; it must never create or expand a spec.
type ColumnSpec struct {
	Name       string
	SQLiteType string
	NotNull    bool
	PKOrder    int
}

type ColumnMetadata struct {
	Name       string
	SQLiteType string
	NotNull    bool
	PKOrder    int
}

type TableSpec struct {
	Name    string
	Columns []ColumnSpec
}

type SchemaSnapshot struct {
	Tables map[string]TableSnapshot
}

type FailureLocation struct {
	Table          string
	PrimaryKeyHash string
	Column         string
	Reason         string
}

type TableReport struct {
	Name     string
	RowCount int64
}

type Report struct {
	CandidateSHA string
	Stage        string
	SourceType   DatabaseType
	TargetType   DatabaseType
	LogScope     LogScope
	Allowlist    string
	Tables       []TableReport
	Failures     []FailureLocation
}

type Profile struct {
	Name         string
	SourceTables []string
	TargetTables []string
	SourceSpecs  []TableSpec
}

var sqlite34SourceTables = []string{
	"abilities", "auth_flows", "authz_roles", "casbin_rules", "channels",
	"checkins", "custom_oauth_providers", "external_identity_claims", "logs",
	"midjourneys", "models", "options", "passkey_credentials", "perf_metrics",
	"prefill_groups", "quota_data", "redemptions", "setups", "subscription_plans",
	"subscription_pre_consume_records", "system_instances", "system_task_locks",
	"system_tasks", "tasks", "tokens", "two_fa_backup_codes", "two_fas",
	"user_oauth_bindings", "user_sessions", "user_subscriptions", "vendors", "users",
	"top_ups", "subscription_orders",
}

var sqlite34TargetTables = append(append([]string{}, sqlite34SourceTables...),
	"api_key_deliveries", "enterprises", "enterprise_memberships", "enterprise_invitations",
	"enterprise_ledgers", "enterprise_usage_records")

// SQLite34PreEnterprise returns a copy of the fixed table allowlist. Callers
// cannot mutate the package-level profile by changing the returned slices.
func SQLite34PreEnterprise() Profile {
	sourceTables := append([]string(nil), sqlite34SourceTables...)
	sourceSpecs := make([]TableSpec, len(sourceTables))
	for i, name := range sourceTables {
		sourceSpecs[i] = TableSpec{Name: name}
	}
	return Profile{
		Name:         AllowlistSQLite34,
		SourceTables: sourceTables,
		TargetTables: append([]string(nil), sqlite34TargetTables...),
		SourceSpecs:  sourceSpecs,
	}
}

// Preflight validates only metadata. It never performs DDL/DML and therefore
// has no database side effects.
func Preflight(manifest Manifest, source, target SchemaSnapshot) Report {
	report := Report{
		CandidateSHA: manifest.CandidateSHA,
		Stage:        "preflight",
		SourceType:   manifest.SourceType,
		TargetType:   manifest.TargetType,
		LogScope:     manifest.LogScope,
		Allowlist:    manifest.AllowlistVersion,
	}
	fail := func(table, reason string) {
		report.Failures = append(report.Failures, FailureLocation{Table: table, Reason: reason})
	}

	if strings.TrimSpace(manifest.CandidateSHA) == "" {
		fail("", "candidate_sha_required")
	}
	if manifest.SourceType != SourceSQLite && manifest.SourceType != SourceMySQL {
		fail("", "unsupported_source_type")
	}
	if manifest.TargetType != TargetPostgres {
		fail("", "unsupported_target_type")
	}
	if manifest.SourceType == DatabaseType(manifest.TargetType) && manifest.SourceType != "" {
		fail("", "source_target_must_differ")
	}
	if !validLogScope(manifest.LogScope) {
		fail("", "log_scope_required")
	}
	if manifest.AllowlistVersion != AllowlistSQLite34 {
		fail("", "unsupported_allowlist")
	}
	if manifest.BatchSize <= 0 {
		fail("", "batch_size_required")
	}
	if !manifest.SnapshotProven {
		fail("", "snapshot_proof_required")
	}

	profile := SQLite34PreEnterprise()
	if !validateSourceSchema(profile, source) {
		fail("", "source_schema_drift")
	}
	if len(target.Tables) != 0 {
		fail("", "target_not_empty")
	}
	if len(report.Failures) == 0 {
		for _, name := range profile.SourceTables {
			report.Tables = append(report.Tables, TableReport{Name: name, RowCount: source.Tables[name].RowCount})
		}
		// Stable output is useful for audit comparison and does not expose rows.
		sort.Slice(report.Tables, func(i, j int) bool { return report.Tables[i].Name < report.Tables[j].Name })
	}
	return report
}

func (r Report) OK() bool { return len(r.Failures) == 0 }

func validLogScope(scope LogScope) bool {
	switch scope {
	case LogPrimary, LogSeparateRetain, LogSeparateMigrate, LogClickHouseRetain:
		return true
	default:
		return false
	}
}

func sameNames(tables map[string]TableSnapshot, expected []string) bool {
	if len(tables) != len(expected) {
		return false
	}
	for _, name := range expected {
		if _, ok := tables[name]; !ok {
			return false
		}
	}
	return true
}

// ValidateSourceSchema compares a source snapshot with a fixed profile. It is
// exported for the later signature-building slice and remains side-effect-free.
func ValidateSourceSchema(profile Profile, source SchemaSnapshot) bool {
	return validateSourceSchema(profile, source)
}

func validateSourceSchema(profile Profile, source SchemaSnapshot) bool {
	if !sameNames(source.Tables, profile.SourceTables) {
		return false
	}
	for _, spec := range profile.SourceSpecs {
		snapshot, ok := source.Tables[spec.Name]
		if !ok || len(spec.Columns) == 0 {
			continue
		}
		if !sameColumns(snapshot.Columns, spec.Columns) {
			return false
		}
	}
	return true
}

func sameColumns(actual []ColumnMetadata, expected []ColumnSpec) bool {
	if len(actual) != len(expected) {
		return false
	}
	for i := range expected {
		if actual[i].Name != expected[i].Name || actual[i].SQLiteType != expected[i].SQLiteType ||
			actual[i].NotNull != expected[i].NotNull || actual[i].PKOrder != expected[i].PKOrder {
			return false
		}
	}
	return true
}

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
	AllowlistSQLite34           = "sqlite-34-pre-enterprise"
	SQLite34ProfileCandidateSHA = "e451c93f1d44a1a84f9bab07937510458c8bd642"
	MaxPreflightBatchSize       = 1000

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
	SourceIdentityHash string
	LogScope           LogScope
	AllowlistVersion   string
	BatchSize          int
	TargetIdentityHash string
	SnapshotProof      string
}

type TableSnapshot struct {
	Columns  []ColumnMetadata
	Indexes  []IndexMetadata
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

type IndexSpec struct {
	Name    string
	Unique  bool
	Origin  string
	Columns []string
}

type IndexMetadata struct {
	Name    string
	Unique  bool
	Origin  string
	Columns []string
}

type TableSpec struct {
	Name    string
	Columns []ColumnSpec
	Indexes []IndexSpec
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

// SQLite34PreEnterprise returns a copy of the fixed table allowlist. Callers
// cannot mutate the package-level profile by changing the returned slices.
func SQLite34PreEnterprise() Profile {
	sourceSpecs := cloneTableSpecs(sqlite34TableSpecs)
	sourceTables := make([]string, len(sourceSpecs))
	for i, spec := range sourceSpecs {
		sourceTables[i] = spec.Name
	}
	return Profile{
		Name:         AllowlistSQLite34,
		SourceTables: sourceTables,
		TargetTables: append(append([]string(nil), sourceTables...), "api_key_deliveries", "enterprises", "enterprise_memberships", "enterprise_invitations", "enterprise_ledgers", "enterprise_usage_records"),
		SourceSpecs:  sourceSpecs,
	}
}

func cloneTableSpecs(specs []TableSpec) []TableSpec {
	result := make([]TableSpec, len(specs))
	for i, spec := range specs {
		result[i] = TableSpec{Name: spec.Name, Columns: append([]ColumnSpec(nil), spec.Columns...), Indexes: make([]IndexSpec, len(spec.Indexes))}
		for j, index := range spec.Indexes {
			result[i].Indexes[j] = IndexSpec{Name: index.Name, Unique: index.Unique, Origin: index.Origin, Columns: append([]string(nil), index.Columns...)}
		}
	}
	return result
}

// Preflight validates only metadata. It never performs DDL/DML and therefore
// has no database side effects.
func Preflight(manifest Manifest, source, target SchemaSnapshot) Report {
	return preflight(SQLite34PreEnterprise(), manifest, source, target)
}

func preflight(profile Profile, manifest Manifest, source, target SchemaSnapshot) Report {
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
	if manifest.CandidateSHA != SQLite34ProfileCandidateSHA {
		fail("", "candidate_sha_mismatch")
	}
	if manifest.SourceType != SourceSQLite {
		fail("", "sqlite34_source_only")
	}
	if manifest.TargetType != TargetPostgres {
		fail("", "unsupported_target_type")
	}
	if strings.TrimSpace(manifest.SourceIdentityHash) == "" {
		fail("", "source_identity_hash_required")
	}
	if strings.TrimSpace(manifest.TargetIdentityHash) == "" {
		fail("", "target_identity_hash_required")
	}
	if strings.TrimSpace(manifest.SourceIdentityHash) == strings.TrimSpace(manifest.TargetIdentityHash) && strings.TrimSpace(manifest.SourceIdentityHash) != "" {
		fail("", "source_target_identity_must_differ")
	}
	if manifest.LogScope != LogPrimary {
		fail("", "sqlite34_log_primary_only")
	}
	if manifest.AllowlistVersion != AllowlistSQLite34 {
		fail("", "unsupported_allowlist")
	}
	if manifest.BatchSize <= 0 || manifest.BatchSize > MaxPreflightBatchSize {
		fail("", "batch_size_out_of_range")
	}
	if strings.TrimSpace(manifest.SnapshotProof) == "" {
		fail("", "snapshot_proof_required")
	}

	if !validateProfile(profile) {
		fail("", "incomplete_table_spec")
	} else if !validateSourceSchema(profile, source) {
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

func validateProfile(profile Profile) bool {
	if profile.Name != AllowlistSQLite34 || len(profile.SourceSpecs) != 34 || len(profile.SourceTables) != 34 {
		return false
	}
	seen := make(map[string]struct{}, len(profile.SourceSpecs))
	for _, spec := range profile.SourceSpecs {
		if spec.Name == "" || len(spec.Columns) == 0 {
			return false
		}
		if _, exists := seen[spec.Name]; exists {
			return false
		}
		seen[spec.Name] = struct{}{}
	}
	return true
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

func validateSourceSchema(profile Profile, source SchemaSnapshot) bool {
	if !sameNames(source.Tables, profile.SourceTables) {
		return false
	}
	for _, spec := range profile.SourceSpecs {
		snapshot, ok := source.Tables[spec.Name]
		if !ok {
			return false
		}
		if !sameColumns(snapshot.Columns, spec.Columns) {
			return false
		}
		if !sameIndexes(snapshot.Indexes, spec.Indexes) {
			return false
		}
	}
	return true
}

func sameIndexes(actual []IndexMetadata, expected []IndexSpec) bool {
	if len(actual) != len(expected) {
		return false
	}
	for i := range expected {
		if actual[i].Name != expected[i].Name || actual[i].Unique != expected[i].Unique || actual[i].Origin != expected[i].Origin || len(actual[i].Columns) != len(expected[i].Columns) {
			return false
		}
		for j := range expected[i].Columns {
			if actual[i].Columns[j] != expected[i].Columns[j] {
				return false
			}
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

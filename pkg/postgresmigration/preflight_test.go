package postgresmigration

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const expectedSQLite34SignatureSHA256 = "a307c47470b78e8b4f540c580994db2870970fcd57113a877e7a11e221ec8f17"

var baselineSourceTableNames = []string{
	"abilities", "auth_flows", "authz_roles", "casbin_rule", "channels", "checkins",
	"custom_oauth_providers", "external_identity_claims", "logs", "midjourneys", "models",
	"options", "passkey_credentials", "perf_metrics", "prefill_groups", "quota_data",
	"redemptions", "setups", "subscription_orders", "subscription_plans",
	"subscription_pre_consume_records", "system_instances", "system_task_locks", "system_tasks",
	"tasks", "tokens", "top_ups", "two_fa_backup_codes", "two_fas", "user_oauth_bindings",
	"user_sessions", "user_subscriptions", "users", "vendors",
}

var baselineTargetTableNames = []string{
	"abilities", "auth_flows", "authz_roles", "casbin_rule", "channels", "checkins",
	"custom_oauth_providers", "external_identity_claims", "logs", "midjourneys", "models",
	"options", "passkey_credentials", "perf_metrics", "prefill_groups", "quota_data",
	"redemptions", "setups", "subscription_orders", "subscription_plans",
	"subscription_pre_consume_records", "system_instances", "system_task_locks", "system_tasks",
	"tasks", "tokens", "top_ups", "two_fa_backup_codes", "two_fas", "user_oauth_bindings",
	"user_sessions", "user_subscriptions", "users", "vendors", "api_key_deliveries",
	"enterprises", "enterprise_memberships", "enterprise_invitations", "enterprise_ledgers",
	"enterprise_usage_records",
}

func validManifest() Manifest {
	return Manifest{
		CandidateSHA:       SQLite34ProfileCandidateSHA,
		SourceType:         SourceSQLite,
		TargetType:         TargetPostgres,
		SourceIdentityHash: "source-hash",
		LogScope:           LogPrimary,
		AllowlistVersion:   AllowlistSQLite34,
		BatchSize:          100,
		TargetIdentityHash: "target-hash",
		SnapshotProof:      "synthetic-fixture",
	}
}

func source34() SchemaSnapshot {
	p := SQLite34PreEnterprise()
	s := SchemaSnapshot{Tables: make(map[string]TableSnapshot, len(baselineSourceTableNames))}
	specs := make(map[string]TableSpec, len(p.SourceSpecs))
	for _, spec := range p.SourceSpecs {
		specs[spec.Name] = spec
	}
	for _, name := range baselineSourceTableNames {
		spec, ok := specs[name]
		if !ok {
			panic("static baseline table missing from profile: " + name)
		}
		columns := make([]ColumnMetadata, len(spec.Columns))
		for i, column := range spec.Columns {
			columns[i] = ColumnMetadata{Name: column.Name, SQLiteType: column.SQLiteType, NotNull: column.NotNull, PKOrder: column.PKOrder}
		}
		indexes := make([]IndexMetadata, len(spec.Indexes))
		for i, index := range spec.Indexes {
			indexes[i] = IndexMetadata{Name: index.Name, Unique: index.Unique, Origin: index.Origin, Columns: append([]string(nil), index.Columns...)}
		}
		s.Tables[spec.Name] = TableSnapshot{Columns: columns, Indexes: indexes, RowCount: 1}
	}
	return s
}

func TestPostgresPrimaryMigrationSQLite34StaticBaseline(t *testing.T) {
	profile := SQLite34PreEnterprise()
	require.Equal(t, baselineSourceTableNames, profile.SourceTables)
	require.Equal(t, baselineTargetTableNames, profile.TargetTables)
	assert.Equal(t, expectedSQLite34SignatureSHA256, sqlite34SignatureFingerprint(profile))
}

func sqlite34SignatureFingerprint(profile Profile) string {
	lines := make([]string, 0, len(profile.SourceSpecs))
	for _, spec := range profile.SourceSpecs {
		columns := make([]string, len(spec.Columns))
		for i, column := range spec.Columns {
			notNull := "nn0"
			if column.NotNull {
				notNull = "nn1"
			}
			columns[i] = fmt.Sprintf("%s:%s:%s:pk%d", column.Name, column.SQLiteType, notNull, column.PKOrder)
		}
		indexes := make([]string, len(spec.Indexes))
		for i, index := range spec.Indexes {
			unique := "u0"
			if index.Unique {
				unique = "u1"
			}
			indexes[i] = fmt.Sprintf("%s[%s,%s]:%s", index.Name, unique, index.Origin, strings.Join(index.Columns, ","))
		}
		lines = append(lines, fmt.Sprintf("%s|%s|%s", spec.Name, strings.Join(columns, ";"), strings.Join(indexes, ";")))
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

func TestPostgresPrimaryMigrationPreflightAcceptsSQLite34Metadata(t *testing.T) {
	report := Preflight(validManifest(), source34(), SchemaSnapshot{})
	require.True(t, report.OK())
	assert.Len(t, report.Tables, 34)
	assert.Empty(t, report.Failures)
}

func TestPostgresPrimaryMigrationPreflightRejectsSchemaDrift(t *testing.T) {
	source := source34()
	delete(source.Tables, "users")
	source.Tables["enterprises"] = TableSnapshot{}

	report := Preflight(validManifest(), source, SchemaSnapshot{})
	require.False(t, report.OK())
	assert.Contains(t, report.Failures, FailureLocation{Reason: "source_schema_drift"})
	assert.Empty(t, report.Tables)
}

func TestPostgresPrimaryMigrationPreflightRejectsNonEmptyTargetWithoutMutation(t *testing.T) {
	source := source34()
	target := SchemaSnapshot{Tables: map[string]TableSnapshot{
		"users": {RowCount: 1},
	}}
	before := cloneSnapshot(target)

	report := Preflight(validManifest(), source, target)
	require.False(t, report.OK())
	assert.Contains(t, report.Failures, FailureLocation{Reason: "target_not_empty"})
	assert.Equal(t, before, target)
}

func TestPostgresPrimaryMigrationPreflightRejectsUnprovenSnapshot(t *testing.T) {
	manifest := validManifest()
	manifest.SnapshotProof = ""
	report := Preflight(manifest, source34(), SchemaSnapshot{})
	require.False(t, report.OK())
	assert.Contains(t, report.Failures, FailureLocation{Reason: "snapshot_proof_required"})
}

func TestPostgresPrimaryMigrationPreflightRejectsColumnSignatureDrift(t *testing.T) {
	profile := SQLite34PreEnterprise()
	base := source34()
	for _, mutate := range []func([]ColumnMetadata) []ColumnMetadata{
		func(columns []ColumnMetadata) []ColumnMetadata {
			columns[0].SQLiteType = "TEXT"
			return columns
		},
		func(columns []ColumnMetadata) []ColumnMetadata {
			columns[0].NotNull = !columns[0].NotNull
			return columns
		},
		func(columns []ColumnMetadata) []ColumnMetadata {
			columns[0].PKOrder = 0
			return columns
		},
		func(columns []ColumnMetadata) []ColumnMetadata { return columns[1:] },
		func(columns []ColumnMetadata) []ColumnMetadata {
			return append(columns, ColumnMetadata{Name: "unexpected"})
		},
	} {
		source := source34()
		columns := append([]ColumnMetadata(nil), base.Tables["abilities"].Columns...)
		source.Tables["abilities"] = TableSnapshot{Columns: mutate(columns)}
		assert.False(t, validateSourceSchema(profile, source))
	}
}

func TestPostgresPrimaryMigrationPreflightRejectsSameSourceTargetIdentity(t *testing.T) {
	manifest := validManifest()
	manifest.TargetIdentityHash = manifest.SourceIdentityHash
	report := Preflight(manifest, source34(), SchemaSnapshot{})
	require.False(t, report.OK())
	assert.Contains(t, report.Failures, FailureLocation{Reason: "source_target_identity_must_differ"})
}

func TestPostgresPrimaryMigrationPreflightRejectsIndexSignatureDrift(t *testing.T) {
	source := source34()
	index := source.Tables["users"].Indexes[0]
	index.Columns[0] = "unexpected"
	source.Tables["users"] = TableSnapshot{Columns: source.Tables["users"].Columns, Indexes: append([]IndexMetadata{index}, source.Tables["users"].Indexes[1:]...)}
	report := Preflight(validManifest(), source, SchemaSnapshot{})
	require.False(t, report.OK())
	assert.Contains(t, report.Failures, FailureLocation{Reason: "source_schema_drift"})
}

func TestPostgresPrimaryMigrationPreflightRejectsIncompleteProfile(t *testing.T) {
	profile := SQLite34PreEnterprise()
	profile.SourceSpecs[0].Columns = nil
	assert.False(t, validateProfile(profile))
}

func TestPostgresPrimaryMigrationPreflightRejectsUnsupportedSQLite34SourceOrLog(t *testing.T) {
	for _, mutate := range []func(*Manifest){
		func(manifest *Manifest) { manifest.SourceType = SourceMySQL },
		func(manifest *Manifest) { manifest.LogScope = LogSeparateRetain },
	} {
		manifest := validManifest()
		mutate(&manifest)
		report := Preflight(manifest, source34(), SchemaSnapshot{})
		require.False(t, report.OK())
	}
}

func TestPostgresPrimaryMigrationPreflightRejectsMissingProofHashCandidateAndOversizedBatch(t *testing.T) {
	for _, mutate := range []func(*Manifest){
		func(manifest *Manifest) { manifest.SourceIdentityHash = "" },
		func(manifest *Manifest) { manifest.TargetIdentityHash = "" },
		func(manifest *Manifest) { manifest.SnapshotProof = "" },
		func(manifest *Manifest) { manifest.CandidateSHA = "wrong" },
		func(manifest *Manifest) { manifest.BatchSize = MaxPreflightBatchSize + 1 },
	} {
		manifest := validManifest()
		mutate(&manifest)
		report := Preflight(manifest, source34(), SchemaSnapshot{})
		require.False(t, report.OK())
	}
}

func TestPostgresPrimaryMigrationPreflightReportDoesNotContainIdentityOrProof(t *testing.T) {
	manifest := validManifest()
	manifest.SourceIdentityHash = "source-secret-hash"
	manifest.TargetIdentityHash = "target-secret-hash"
	manifest.SnapshotProof = "fixture-proof-secret"
	manifest.CandidateSHA = "wrong"
	report := Preflight(manifest, source34(), SchemaSnapshot{})
	text := fmt.Sprintf("%+v", report)
	assert.NotContains(t, text, "source-secret-hash")
	assert.NotContains(t, text, "target-secret-hash")
	assert.NotContains(t, text, "fixture-proof-secret")
}

func cloneSnapshot(snapshot SchemaSnapshot) SchemaSnapshot {
	clone := SchemaSnapshot{Tables: make(map[string]TableSnapshot, len(snapshot.Tables))}
	for name, table := range snapshot.Tables {
		clone.Tables[name] = TableSnapshot{
			Columns:  append([]ColumnMetadata(nil), table.Columns...),
			Indexes:  append([]IndexMetadata(nil), table.Indexes...),
			RowCount: table.RowCount,
		}
	}
	return clone
}

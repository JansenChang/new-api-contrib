package postgresmigration

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	s := SchemaSnapshot{Tables: make(map[string]TableSnapshot, len(p.SourceTables))}
	for _, spec := range p.SourceSpecs {
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
		assert.False(t, ValidateSourceSchema(profile, source))
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
	report := PreflightWithProfile(profile, validManifest(), source34(), SchemaSnapshot{})
	require.False(t, report.OK())
	assert.Contains(t, report.Failures, FailureLocation{Reason: "incomplete_table_spec"})
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

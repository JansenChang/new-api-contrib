package postgresmigration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validManifest() Manifest {
	return Manifest{
		CandidateSHA:       "e451c93f1d44a1a84f9bab07937510458c8bd642",
		SourceType:         SourceSQLite,
		TargetType:         TargetPostgres,
		LogScope:           LogPrimary,
		AllowlistVersion:   AllowlistSQLite34,
		BatchSize:          100,
		TargetIdentityHash: "target-hash",
		SnapshotProven:     true,
	}
}

func source34() SchemaSnapshot {
	p := SQLite34PreEnterprise()
	s := SchemaSnapshot{Tables: make(map[string]TableSnapshot, len(p.SourceTables))}
	for _, name := range p.SourceTables {
		s.Tables[name] = TableSnapshot{RowCount: 1}
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
	before := target

	report := Preflight(validManifest(), source, target)
	require.False(t, report.OK())
	assert.Contains(t, report.Failures, FailureLocation{Reason: "target_not_empty"})
	assert.Equal(t, before, target)
}

func TestPostgresPrimaryMigrationPreflightRejectsUnprovenSnapshot(t *testing.T) {
	manifest := validManifest()
	manifest.SnapshotProven = false
	report := Preflight(manifest, source34(), SchemaSnapshot{})
	require.False(t, report.OK())
	assert.Contains(t, report.Failures, FailureLocation{Reason: "snapshot_proof_required"})
}

func TestPostgresPrimaryMigrationPreflightRejectsColumnSignatureDrift(t *testing.T) {
	profile := SQLite34PreEnterprise()
	profile.SourceSpecs[0].Columns = []ColumnSpec{{Name: "id", SQLiteType: "INTEGER", PKOrder: 1}}
	source := source34()
	source.Tables["abilities"] = TableSnapshot{Columns: []ColumnMetadata{{Name: "id", SQLiteType: "TEXT", PKOrder: 1}}}

	assert.False(t, ValidateSourceSchema(profile, source))
}

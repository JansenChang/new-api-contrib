package postgresmigration

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestCollectSQLite34SnapshotFeedsPreflight(t *testing.T) {
	db, err := sql.Open("sqlite", "file:sqlite34-collector?mode=memory&cache=shared")
	require.NoError(t, err)
	defer db.Close()
	createSQLite34Fixture(t, db)

	snapshot, err := CollectSQLite34Snapshot(context.Background(), db)
	require.NoError(t, err)
	require.Equal(t, source34(), snapshot)
	require.True(t, Preflight(validManifest(), snapshot, SchemaSnapshot{}).OK())
}

func TestCollectSQLite34SnapshotRejectsMissingTableAndIndexDrift(t *testing.T) {
	db, err := sql.Open("sqlite", "file:sqlite34-collector-drift?mode=memory&cache=shared")
	require.NoError(t, err)
	defer db.Close()
	createSQLite34Fixture(t, db)

	_, err = db.Exec(`DROP TABLE "users"`)
	require.NoError(t, err)
	_, err = CollectSQLite34Snapshot(context.Background(), db)
	require.Error(t, err)

	db3, err := sql.Open("sqlite", "file:sqlite34-collector-extra?mode=memory&cache=shared")
	require.NoError(t, err)
	defer db3.Close()
	createSQLite34Fixture(t, db3)
	_, err = db3.Exec(`CREATE TABLE "enterprises" ("id" INTEGER PRIMARY KEY)`)
	require.NoError(t, err)
	_, err = CollectSQLite34Snapshot(context.Background(), db3)
	require.Error(t, err)

	db2, err := sql.Open("sqlite", "file:sqlite34-collector-index-drift?mode=memory&cache=shared")
	require.NoError(t, err)
	defer db2.Close()
	createSQLite34Fixture(t, db2)
	_, err = db2.Exec(`DROP INDEX "idx_users_username"`)
	require.NoError(t, err)
	snapshot, err = CollectSQLite34Snapshot(context.Background(), db2)
	require.NoError(t, err)
	require.False(t, Preflight(validManifest(), snapshot, SchemaSnapshot{}).OK())
}

func TestPostgresPrimaryMigrationPreflightRejectsPartialIndexDrift(t *testing.T) {
	source := source34()
	index := source.Tables["users"].Indexes[0]
	index.Partial = true
	source.Tables["users"] = TableSnapshot{Columns: source.Tables["users"].Columns, Indexes: append([]IndexMetadata{index}, source.Tables["users"].Indexes[1:]...)}
	require.False(t, Preflight(validManifest(), source, SchemaSnapshot{}).OK())
}

func createSQLite34Fixture(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, spec := range SQLite34PreEnterprise().SourceSpecs {
		columns := make([]string, len(spec.Columns))
		for i, column := range spec.Columns {
			columns[i] = quoteSQLiteIdentifier(column.Name) + " " + column.SQLiteType
			if column.NotNull {
				columns[i] += " NOT NULL"
			}
		}
		for _, index := range spec.Indexes {
			if index.Origin == "pk" {
				columns = append(columns, "PRIMARY KEY ("+quotedColumns(index.Columns)+")")
			} else if index.Origin == "u" {
				columns = append(columns, "UNIQUE ("+quotedColumns(index.Columns)+")")
			}
		}
		_, err := db.Exec("CREATE TABLE " + quoteSQLiteIdentifier(spec.Name) + " (" + strings.Join(columns, ", ") + ")")
		require.NoError(t, err)
		for i := len(spec.Indexes) - 1; i >= 0; i-- {
			index := spec.Indexes[i]
			if index.Origin != "c" {
				continue
			}
			unique := ""
			if index.Unique {
				unique = "UNIQUE "
			}
			_, err := db.Exec(fmt.Sprintf("CREATE %sINDEX %s ON %s (%s)", unique, quoteSQLiteIdentifier(index.Name), quoteSQLiteIdentifier(spec.Name), quotedColumns(index.Columns)))
			require.NoError(t, err)
		}
	}
}

func quotedColumns(columns []string) string {
	quoted := make([]string, len(columns))
	for i, column := range columns {
		quoted[i] = quoteSQLiteIdentifier(column)
	}
	return strings.Join(quoted, ", ")
}

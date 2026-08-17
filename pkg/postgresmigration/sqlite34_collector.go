package postgresmigration

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

// CollectSQLite34Snapshot reads only the fixed SQLite-34 metadata contract.
// The caller owns db; this function never opens a connection or writes SQL.
func CollectSQLite34Snapshot(ctx context.Context, db *sql.DB) (SchemaSnapshot, error) {
	if ctx == nil {
		return SchemaSnapshot{}, fmt.Errorf("context is nil")
	}
	if db == nil {
		return SchemaSnapshot{}, fmt.Errorf("database is nil")
	}

	profile := SQLite34PreEnterprise()
	if err := validateSQLite34TableSet(ctx, db, profile.SourceTables); err != nil {
		return SchemaSnapshot{}, err
	}
	snapshot := SchemaSnapshot{Tables: make(map[string]TableSnapshot, len(profile.SourceSpecs))}
	for _, spec := range profile.SourceSpecs {
		table := quoteSQLiteIdentifier(spec.Name)
		columns, err := collectSQLite34Columns(ctx, db, table)
		if err != nil {
			return SchemaSnapshot{}, fmt.Errorf("table %q columns: %w", spec.Name, err)
		}
		indexes, err := collectSQLite34Indexes(ctx, db, table, spec)
		if err != nil {
			return SchemaSnapshot{}, fmt.Errorf("table %q indexes: %w", spec.Name, err)
		}
		var rowCount int64
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&rowCount); err != nil {
			return SchemaSnapshot{}, fmt.Errorf("table %q row count: %w", spec.Name, err)
		}
		snapshot.Tables[spec.Name] = TableSnapshot{Columns: columns, Indexes: indexes, RowCount: rowCount}
	}
	return snapshot, nil
}

func validateSQLite34TableSet(ctx context.Context, db *sql.DB, expected []string) error {
	rows, err := db.QueryContext(ctx, "SELECT name FROM main.sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return fmt.Errorf("enumerate SQLite tables: %w", err)
	}
	defer rows.Close()
	actual := make([]string, 0, len(expected))
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("enumerate SQLite tables: %w", err)
		}
		actual = append(actual, name)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("enumerate SQLite tables: %w", err)
	}
	if len(actual) != len(expected) {
		return fmt.Errorf("SQLite-34 table set drift: expected %d tables, found %d", len(expected), len(actual))
	}
	for _, name := range expected {
		found := sort.SearchStrings(actual, name)
		if found == len(actual) || actual[found] != name {
			return fmt.Errorf("SQLite-34 table set drift: unexpected or missing table %q", name)
		}
	}
	return nil
}

func collectSQLite34Columns(ctx context.Context, db *sql.DB, table string) ([]ColumnMetadata, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []ColumnMetadata
	for rows.Next() {
		var cid, notNull, pk int
		var name, sqliteType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &sqliteType, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns = append(columns, ColumnMetadata{Name: name, SQLiteType: sqliteType, NotNull: notNull != 0, PKOrder: pk})
	}
	return columns, rows.Err()
}

func collectSQLite34Indexes(ctx context.Context, db *sql.DB, table string, spec TableSpec) ([]IndexMetadata, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA index_list("+table+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type indexInfo struct {
		seq             int
		name, origin    string
		unique, partial bool
	}
	var infos []indexInfo
	for rows.Next() {
		var seq, unique, partial int
		var name, origin string
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			return nil, err
		}
		infos = append(infos, indexInfo{seq: seq, name: name, unique: unique != 0, origin: origin, partial: partial != 0})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].seq < infos[j].seq })
	indexes := make([]IndexMetadata, 0, len(infos))
	for _, info := range infos {
		indexName := ""
		for _, expected := range spec.Indexes {
			if expected.Name == info.name {
				indexName = expected.Name
				break
			}
		}
		if indexName == "" {
			return nil, fmt.Errorf("unexpected index %q", info.name)
		}
		columns, err := collectSQLite34IndexColumns(ctx, db, quoteSQLiteIdentifier(indexName))
		if err != nil {
			return nil, fmt.Errorf("index %q: %w", info.name, err)
		}
		indexes = append(indexes, IndexMetadata{Name: info.name, Unique: info.unique, Origin: info.origin, Partial: info.partial, Columns: columns})
	}
	return indexes, nil
}

func collectSQLite34IndexColumns(ctx context.Context, db *sql.DB, index string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA index_info("+index+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type columnInfo struct {
		seq  int
		name sql.NullString
	}
	var infos []columnInfo
	for rows.Next() {
		var seq, cid int
		var name sql.NullString
		if err := rows.Scan(&seq, &cid, &name); err != nil {
			return nil, err
		}
		infos = append(infos, columnInfo{seq: seq, name: name})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].seq < infos[j].seq })
	columns := make([]string, 0, len(infos))
	for _, info := range infos {
		if !info.name.Valid {
			return nil, fmt.Errorf("index column is expression")
		}
		columns = append(columns, info.name.String)
	}
	return columns, nil
}

func quoteSQLiteIdentifier(identifier string) string {
	result := `"`
	for _, char := range identifier {
		if char == '"' {
			result += `""`
		} else {
			result += string(char)
		}
	}
	return result + `"`
}

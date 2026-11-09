package api

import "database/sql"

// SourceDB is a database in another tool's MySQL.
type SourceDB struct {
	Name   string  `json:"name"`
	Tables int     `json:"tables"`
	SizeMB float64 `json:"size_mb"`
}

func listSourceDBs(db *sql.DB) ([]SourceDB, error) {
	rows, err := db.Query(`SELECT s.schema_name, COUNT(t.table_name), COALESCE(SUM(t.data_length + t.index_length), 0)
		FROM information_schema.schemata s LEFT JOIN information_schema.tables t ON t.table_schema = s.schema_name
		WHERE s.schema_name NOT IN ('mysql','information_schema','performance_schema','sys')
		GROUP BY s.schema_name ORDER BY s.schema_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SourceDB{}
	for rows.Next() {
		var d SourceDB
		var size float64
		if err := rows.Scan(&d.Name, &d.Tables, &size); err != nil {
			return nil, err
		}
		d.SizeMB = float64(int(size/1048576*10)) / 10
		out = append(out, d)
	}
	return out, rows.Err()
}

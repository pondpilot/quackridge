package policy

import (
	"testing"
)

func TestAdversarialPolicy(t *testing.T) {
	evaluator, err := NewEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	defer evaluator.Close()
	tests := []struct {
		name  string
		query string
		allow bool
	}{
		{"simple read", "SELECT 1", true},
		{"joined CTE", "WITH x AS (SELECT * FROM warehouse.sales.orders) SELECT count(*) FROM x", true},
		{"unicode in string", "SELECT 'em space: '", true},
		{"metadata", "SELECT * FROM quackridge_metadata_v1()", true},
		{"identity shorthand", "FROM whoami()", true},
		{"range", "SELECT sum(i) FROM range(10) t(i)", true},
		{"transaction", "BEGIN TRANSACTION", true},
		{"temporary table", "CREATE TEMP TABLE qr_tmp_result AS SELECT 42 AS value", true},
		{"multiple", "SELECT 1; SELECT 2", false},
		{"comment hidden DML", "/* read */ INSERT INTO t VALUES (1)", false},
		{"CTE DML", "WITH x AS (SELECT 1) INSERT INTO t SELECT * FROM x", false},
		{"persistent DDL", "CREATE TABLE stolen AS SELECT 1", false},
		{"unscoped temp", "CREATE TEMP TABLE stolen AS SELECT 1", false},
		{"temp nested filesystem", "CREATE TEMP TABLE qr_tmp_bad AS SELECT * FROM read_csv('/etc/passwd')", false},
		{"filesystem table function", "SELECT * FROM read_csv('/etc/passwd')", false},
		{"quoted replacement scan", `SELECT * FROM '/etc/passwd.csv'`, false},
		{"network table function", "SELECT * FROM read_json_auto('https://example.com/private')", false},
		{"PostgreSQL pass-through", "SELECT * FROM postgres_query('warehouse', 'DROP TABLE sales.orders')", false},
		{"dynamic SQL", "SELECT * FROM query('DELETE FROM t')", false},
		{"attach", "ATTACH 'x.duckdb' AS x", false},
		{"detach", "DETACH warehouse", false},
		{"install", "INSTALL httpfs", false},
		{"load", "LOAD httpfs", false},
		{"secret", "CREATE SECRET x (TYPE S3, KEY_ID 'x')", false},
		{"configuration", "SET memory_limit='100GB'", false},
		{"prepared", "PREPARE danger AS DELETE FROM t", false},
		{"quoted identifier trick", `SELECT * FROM "read_csv('/etc/passwd')"`, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := evaluator.Allow(t.Context(), test.query); got != test.allow {
				t.Fatalf("Allow(%q) = %v, want %v", test.query, got, test.allow)
			}
		})
	}
}

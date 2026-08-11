//go:build integration

package policy

import (
	"database/sql"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"
)

func TestDuckDBParserShape(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, query := range []string{
		"SELECT 1",
		"WITH x AS (SELECT 1) INSERT INTO t SELECT * FROM x",
		"SELECT * FROM read_csv('/etc/passwd')",
		"CREATE TEMP TABLE t AS SELECT 1",
		"SET memory_limit='100GB'",
	} {
		var parsed string
		if err := db.QueryRowContext(t.Context(), "SELECT CAST(json_serialize_sql(CAST(? AS VARCHAR)) AS VARCHAR)", query).Scan(&parsed); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		t.Logf("%s => %s", query, parsed)
	}
}

func TestRegisteredFunctionVisibleAcrossConnections(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handle, err := Install(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	second, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var allowed bool
	if err := second.QueryRowContext(t.Context(), "SELECT quackridge_authorize('', 'FROM whoami()')").Scan(&allowed); err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("registered authorization function rejected safe query")
	}
}

//go:build integration

package engine_test

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	quackridge "github.com/pondpilot/quackridge"
	"github.com/pondpilot/quackridge/internal/engine"
	"github.com/pondpilot/quackridge/internal/source"
	"github.com/pondpilot/quackridge/internal/source/filedb"
	"github.com/pondpilot/quackridge/internal/source/odbc"
)

func TestFileConnectorsAndODBCJoin(t *testing.T) {
	extensionDir := os.Getenv("QUACKRIDGE_EXTENSION_DIR")
	if extensionDir == "" {
		t.Skip("QUACKRIDGE_EXTENSION_DIR is required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	directory := t.TempDir()
	duckPath := filepath.Join(directory, "commerce.duckdb")
	sqlitePath := filepath.Join(directory, "support.sqlite")

	fixture, err := sql.Open("duckdb", duckPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.ExecContext(ctx, "CREATE SCHEMA sales; CREATE TABLE sales.orders(id INTEGER, amount INTEGER); INSERT INTO sales.orders VALUES (1, 25), (2, 40)"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.ExecContext(ctx, "LOAD '"+strings.ReplaceAll(filepath.Join(extensionDir, "sqlite_scanner.duckdb_extension"), "'", "''")+"'"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.ExecContext(ctx, "ATTACH '"+strings.ReplaceAll(sqlitePath, "'", "''")+"' AS support (TYPE sqlite); CREATE TABLE support.ticket(order_id INTEGER, state VARCHAR); INSERT INTO support.ticket VALUES (1, 'open'), (2, 'closed'); DETACH support"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Close(); err != nil {
		t.Fatal(err)
	}

	runtime := engine.New()
	if _, err := runtime.Start(ctx, quackridge.Options{ExtensionDir: extensionDir, AllowedPaths: []string{duckPath, sqlitePath}}); err != nil {
		t.Fatal(err)
	}
	defer runtime.Stop(context.Background())
	for _, candidate := range []struct {
		adapter    *filedb.Adapter
		definition source.Definition
	}{
		{filedb.New(runtime, "duckdb", filedb.Config{Path: duckPath}), source.Definition{ID: "commerce", Name: "Commerce", Alias: "commerce", ConnectorType: "duckdb", DatabaseType: "duckdb", Enabled: true}},
		{filedb.New(runtime, "sqlite", filedb.Config{Path: sqlitePath}), source.Definition{ID: "support", Name: "Support", Alias: "support", ConnectorType: "sqlite", DatabaseType: "sqlite", Enabled: true}},
	} {
		if err := candidate.adapter.Attach(ctx, candidate.definition); err != nil {
			t.Fatal(err)
		}
	}
	var total int
	if err := runtime.QueryRow(ctx, `SELECT sum(o.amount)::INTEGER FROM commerce.sales.orders o JOIN support.main.ticket t ON t.order_id = o.id WHERE t.state = 'open'`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 25 {
		t.Fatalf("mixed file join total = %d", total)
	}

	if _, err := exec.LookPath("odbcinst"); err != nil {
		t.Skip("unixODBC is required")
	}
	output, err := exec.CommandContext(ctx, "odbcinst", "-q", "-d").Output()
	if err != nil {
		t.Skip("ODBC driver registry is unavailable")
	}
	driver := ""
	for _, line := range strings.Split(string(output), "\n") {
		name := strings.Trim(strings.TrimSpace(line), "[]")
		if strings.EqualFold(name, "SQLite3") || strings.EqualFold(name, "SQLite") {
			driver = name
			break
		}
	}
	if driver == "" {
		t.Skip("SQLite ODBC driver is required")
	}
	odbcAdapter := odbc.New(runtime, odbc.Config{Driver: driver, DatabaseType: "sqlite", Properties: map[string]string{"Database": sqlitePath}}, odbc.Credential{})
	odbcDefinition := source.Definition{ID: "support_odbc", Name: "Support ODBC", Alias: "support_odbc", ConnectorType: "odbc", DatabaseType: "sqlite", Enabled: true}
	if err := odbcAdapter.Attach(ctx, odbcDefinition); err != nil {
		t.Fatal(err)
	}
	if err := runtime.QueryRow(ctx, `SELECT sum(o.amount)::INTEGER FROM commerce.sales.orders o JOIN support_odbc.main.ticket t ON t.order_id = o.id WHERE t.state = 'closed'`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 40 {
		t.Fatalf("ODBC mixed join total = %d", total)
	}
}

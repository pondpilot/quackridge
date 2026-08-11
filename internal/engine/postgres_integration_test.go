//go:build integration

package engine_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	quackridge "github.com/pondpilot/quackridge"
	"github.com/pondpilot/quackridge/internal/engine"
	"github.com/pondpilot/quackridge/internal/source"
	"github.com/pondpilot/quackridge/internal/source/postgres"
)

func TestPostgresServerSideJoinAndMetadata(t *testing.T) {
	extensionDir := os.Getenv("QUACKRIDGE_EXTENSION_DIR")
	if extensionDir == "" {
		t.Skip("QUACKRIDGE_EXTENSION_DIR is required")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	container := "quackridge-test-" + strconv.Itoa(os.Getpid())
	adminPassword := "temporary-admin-password"
	readerPassword := "temporary-reader-password"
	command := exec.CommandContext(ctx, "docker", "run", "--rm", "-d", "--name", container,
		"-e", "POSTGRES_PASSWORD", "-p", "127.0.0.1::5432", "postgres:17-alpine")
	command.Env = append(os.Environ(), "POSTGRES_PASSWORD="+adminPassword)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start postgres: %v: %s", err, output)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "stop", "-t", "1", container).Run() })

	var port int
	for ctx.Err() == nil {
		output, err := exec.CommandContext(ctx, "docker", "port", container, "5432/tcp").Output()
		if err == nil {
			parts := strings.Split(strings.TrimSpace(string(output)), ":")
			port, _ = strconv.Atoi(parts[len(parts)-1])
		}
		if port > 0 && exec.CommandContext(ctx, "docker", "exec", container, "psql", "-U", "postgres", "-Atqc", "SELECT 1").Run() == nil {
			time.Sleep(300 * time.Millisecond)
			if exec.CommandContext(ctx, "docker", "exec", container, "psql", "-U", "postgres", "-Atqc", "SELECT 1").Run() == nil {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if port == 0 {
		t.Fatal("PostgreSQL did not become ready")
	}

	fixture := `
		CREATE SCHEMA sales;
		CREATE TABLE sales.customers (id UUID PRIMARY KEY, name TEXT NOT NULL, tags TEXT[]);
		CREATE TABLE sales.orders (id BIGINT PRIMARY KEY, customer_id UUID REFERENCES sales.customers(id), amount DECIMAL(18,2), placed_at TIMESTAMPTZ, note TEXT NULL);
		INSERT INTO sales.customers VALUES ('00000000-0000-0000-0000-000000000001', 'Ada', ARRAY['priority','east']);
		INSERT INTO sales.orders VALUES (1, '00000000-0000-0000-0000-000000000001', 12.50, '2026-08-11T12:00:00Z', NULL), (2, '00000000-0000-0000-0000-000000000001', 7.25, '2026-08-11T13:00:00Z', 'ok');
		CREATE ROLE qr_reader LOGIN PASSWORD '` + readerPassword + `';
		ALTER ROLE qr_reader SET default_transaction_read_only = on;
		GRANT CONNECT ON DATABASE postgres TO qr_reader;
		GRANT USAGE ON SCHEMA sales TO qr_reader;
		GRANT SELECT ON ALL TABLES IN SCHEMA sales TO qr_reader;`
	psql := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	psql.Stdin = strings.NewReader(fixture)
	if output, err := psql.CombinedOutput(); err != nil {
		t.Fatalf("create fixtures: %v: %s", err, output)
	}

	runtime := engine.New()
	endpoint, err := runtime.Start(ctx, quackridge.Options{ExtensionDir: extensionDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })
	adapter := postgres.New(runtime, postgres.Config{
		Host: "127.0.0.1", Port: port, Database: "postgres", User: "qr_reader", SSLMode: "disable",
	}, postgres.Credential{Password: readerPassword})
	definition := source.Definition{ID: "warehouse", Name: "Warehouse", Alias: "warehouse", Type: "postgres", Enabled: true}
	if err := adapter.Attach(ctx, definition); err != nil {
		t.Fatalf("attach: %v; cause: %v", err, errors.Unwrap(err))
	}

	var customer string
	var total string
	if err := runtime.QueryRow(ctx, `SELECT c.name, SUM(o.amount)::VARCHAR
		FROM warehouse.sales.customers c JOIN warehouse.sales.orders o ON o.customer_id = c.id
		GROUP BY c.name`).Scan(&customer, &total); err != nil {
		t.Fatal(err)
	}
	if customer != "Ada" || total != "19.75" {
		t.Fatalf("joined result = %q %q", customer, total)
	}
	var columns int
	if err := runtime.QueryRow(ctx, `SELECT count(*) FROM quackridge_metadata_v1()
		WHERE source_id = 'warehouse' AND schema_name = 'sales' AND object_name IN ('customers', 'orders')`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 8 {
		t.Fatalf("metadata columns = %d", columns)
	}

	client, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, extension := range []string{"httpfs", "quack"} {
		path := strings.ReplaceAll(extensionDir+"/"+extension+".duckdb_extension", "'", "''")
		if _, err := client.ExecContext(ctx, "LOAD '"+path+"'"); err != nil {
			t.Fatal(err)
		}
	}
	attach := fmt.Sprintf("ATTACH '%s' AS ridge (TYPE quack, TOKEN '%s')", endpoint, runtime.Token())
	if _, err := client.ExecContext(ctx, attach); err != nil {
		t.Fatal(err)
	}
	if err := client.QueryRowContext(ctx, `SELECT total::VARCHAR FROM ridge.query(
		'SELECT SUM(amount) AS total FROM warehouse.sales.orders')`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != "19.75" {
		t.Fatalf("Quack joined total = %s", total)
	}
	if _, err := client.ExecContext(ctx, `FROM ridge.query(
		'INSERT INTO warehouse.sales.orders VALUES (3, NULL, 1, now(), NULL)')`); err == nil {
		t.Fatal("read-only attachment accepted write")
	}
}

//go:build integration

package engine_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl is required for PostgreSQL TLS integration")
	}
	certificateDirectory := t.TempDir()
	certificate := filepath.Join(certificateDirectory, "server.crt")
	privateKey := filepath.Join(certificateDirectory, "server.key")
	generateCertificate := exec.CommandContext(ctx, "openssl", "req", "-new", "-x509", "-days", "1", "-nodes",
		"-out", certificate, "-keyout", privateKey, "-subj", "/CN=localhost")
	if output, err := generateCertificate.CombinedOutput(); err != nil {
		t.Fatalf("generate PostgreSQL TLS certificate: %v: %s", err, output)
	}
	for _, copySpec := range [][2]string{{certificate, container + ":/var/lib/postgresql/data/server.crt"}, {privateKey, container + ":/var/lib/postgresql/data/server.key"}} {
		if output, err := exec.CommandContext(ctx, "docker", "cp", copySpec[0], copySpec[1]).CombinedOutput(); err != nil {
			t.Fatalf("copy PostgreSQL TLS file: %v: %s", err, output)
		}
	}
	tlsPermissions := exec.CommandContext(ctx, "docker", "exec", "-u", "0", container, "sh", "-c",
		"chmod 600 /var/lib/postgresql/data/server.key && chown postgres:postgres /var/lib/postgresql/data/server.crt /var/lib/postgresql/data/server.key")
	if output, err := tlsPermissions.CombinedOutput(); err != nil {
		t.Fatalf("configure PostgreSQL TLS permissions: %v: %s", err, output)
	}
	if output, err := exec.CommandContext(ctx, "docker", "exec", container, "psql", "-U", "postgres", "-v", "ON_ERROR_STOP=1", "-c", "ALTER SYSTEM SET ssl = on").CombinedOutput(); err != nil {
		t.Fatalf("enable PostgreSQL TLS: %v: %s", err, output)
	}
	if output, err := exec.CommandContext(ctx, "docker", "restart", container).CombinedOutput(); err != nil {
		t.Fatalf("restart PostgreSQL with TLS: %v: %s", err, output)
	}
	tlsReady := false
	for ctx.Err() == nil {
		if published, err := exec.CommandContext(ctx, "docker", "port", container, "5432/tcp").Output(); err == nil {
			parts := strings.Split(strings.TrimSpace(string(published)), ":")
			port, _ = strconv.Atoi(parts[len(parts)-1])
		}
		output, err := exec.CommandContext(ctx, "docker", "exec", container, "psql", "-U", "postgres", "-Atqc", "SHOW ssl").Output()
		connection, dialErr := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 200*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
		}
		if err == nil && dialErr == nil && strings.TrimSpace(string(output)) == "on" {
			tlsReady = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !tlsReady {
		t.Fatal("PostgreSQL did not restart with TLS")
	}

	fixture := `
		CREATE SCHEMA sales;
		CREATE TABLE sales.customers (id UUID PRIMARY KEY, name TEXT NOT NULL, tags TEXT[]);
		CREATE TABLE sales.orders (id BIGINT PRIMARY KEY, customer_id UUID REFERENCES sales.customers(id), amount DECIMAL(18,2), placed_at TIMESTAMPTZ, note TEXT NULL);
		INSERT INTO sales.customers VALUES ('00000000-0000-0000-0000-000000000001', 'Ada', ARRAY['priority','east']);
		INSERT INTO sales.orders VALUES (1, '00000000-0000-0000-0000-000000000001', 12.50, '2026-08-11T12:00:00Z', NULL), (2, '00000000-0000-0000-0000-000000000001', 7.25, '2026-08-11T13:00:00Z', 'ok');
		CREATE VIEW sales.customer_totals AS SELECT customer_id, sum(amount) AS total FROM sales.orders GROUP BY customer_id;
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
		Host: "127.0.0.1", Port: port, Database: "postgres", User: "qr_reader", SSLMode: "require",
	}, postgres.Credential{Password: readerPassword})
	definition := source.Definition{ID: "warehouse", Name: "Warehouse", Alias: "warehouse", ConnectorType: "postgres", DatabaseType: "postgres", Enabled: true}
	if err := adapter.Attach(ctx, definition); err != nil {
		t.Fatalf("attach: %v; cause: %v", err, errors.Unwrap(err))
	}
	if warnings, err := adapter.PostureWarnings(ctx, definition); err != nil || len(warnings) != 0 {
		t.Fatalf("read-only role posture warnings = %v, %v", warnings, err)
	}
	if err := adapter.Health(ctx, definition); err != nil {
		t.Fatal(err)
	}
	metadata, err := adapter.Metadata(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	var salesMetadata int
	for _, row := range metadata {
		if row.SchemaName != nil && *row.SchemaName == "sales" {
			salesMetadata++
		}
	}
	if salesMetadata != 10 {
		t.Fatalf("adapter sales metadata rows = %d of %d", salesMetadata, len(metadata))
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
	if err := runtime.QueryRow(ctx, `SELECT count(*) FROM quackridge_metadata_v2()
		WHERE source_id = 'warehouse' AND schema_name = 'sales' AND object_name IN ('customers', 'orders', 'customer_totals')`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 10 {
		t.Fatalf("metadata columns = %d", columns)
	}
	var viewColumns int
	if err := runtime.QueryRow(ctx, `SELECT count(*) FROM quackridge_metadata_v2()
		WHERE source_id = 'warehouse' AND object_name = 'customer_totals' AND object_type = 'view'`).Scan(&viewColumns); err != nil {
		t.Fatal(err)
	}
	if viewColumns != 2 {
		t.Fatalf("metadata view columns = %d", viewColumns)
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
	proxySetup := []string{
		fmt.Sprintf("CREATE TEMPORARY SECRET qr_proxy (TYPE quack, TOKEN '%s', SCOPE '%s')", runtime.Token(), endpoint),
		"ATTACH ':memory:' AS warehouse_remote",
		"CREATE SCHEMA warehouse_remote.sales",
		fmt.Sprintf(`CREATE VIEW warehouse_remote.sales.customers AS
			SELECT * FROM quack_query('%s', 'SELECT * FROM warehouse.sales.customers', disable_ssl => true)`, endpoint),
		fmt.Sprintf(`CREATE VIEW warehouse_remote.sales.orders AS
			SELECT * FROM quack_query('%s', 'SELECT * FROM warehouse.sales.orders', disable_ssl => true)`, endpoint),
	}
	for _, statement := range proxySetup {
		if _, err := client.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	var federatedCustomer string
	if err := client.QueryRowContext(ctx, `
		WITH local_labels(id, label) AS (
			VALUES ('00000000-0000-0000-0000-000000000001'::UUID, 'browser-local')
		)
		SELECT labels.label || ':' || customers.name, sum(orders.amount)::VARCHAR
		FROM local_labels labels
		JOIN warehouse_remote.sales.customers customers USING (id)
		JOIN warehouse_remote.sales.orders orders ON orders.customer_id = customers.id
		GROUP BY labels.label, customers.name`).Scan(&federatedCustomer, &total); err != nil {
		t.Fatal(err)
	}
	if federatedCustomer != "browser-local:Ada" || total != "19.75" {
		t.Fatalf("federated Quack result = %q %q", federatedCustomer, total)
	}
	if err := client.QueryRowContext(ctx, `SELECT customer, total::VARCHAR FROM ridge.query(
		'SELECT c.name AS customer, SUM(o.amount) AS total
		 FROM warehouse.sales.customers c
		 JOIN warehouse.sales.orders o ON o.customer_id = c.id
		 GROUP BY c.name')`).Scan(&customer, &total); err != nil {
		t.Fatal(err)
	}
	if customer != "Ada" || total != "19.75" {
		t.Fatalf("Quack joined result = %q %q", customer, total)
	}
	decomposed := `SELECT count(*)
		FROM ridge.query('SELECT id FROM warehouse.sales.customers') customers
		JOIN ridge.query('SELECT customer_id FROM warehouse.sales.orders') orders
		ON orders.customer_id = customers.id`
	if err := client.QueryRowContext(ctx, decomposed).Scan(new(int)); err == nil {
		t.Fatal("simultaneous remote scans unexpectedly replaced one server-side statement")
	}

	rows, err := client.QueryContext(ctx, `SELECT * FROM ridge.query(
		'SELECT o.amount, o.placed_at, c.tags, o.note, c.id AS customer_id
		 FROM warehouse.sales.customers c
		 JOIN warehouse.sales.orders o ON o.customer_id = c.id
		 WHERE o.id = 1')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	gotTypes := make([]string, len(types))
	for i, columnType := range types {
		gotTypes[i] = strings.ToUpper(columnType.DatabaseTypeName())
	}
	for index, fragment := range []string{"DECIMAL", "TIMESTAMPTZ", "VARCHAR[]", "VARCHAR", "UUID"} {
		if !strings.Contains(gotTypes[index], fragment) {
			t.Fatalf("remote column types = %v; column %d does not contain %q", gotTypes, index, fragment)
		}
	}
	if !rows.Next() {
		t.Fatalf("complex row unavailable: %v", rows.Err())
	}
	var amount, tags, note, customerID any
	var placedAt time.Time
	if err := rows.Scan(&amount, &placedAt, &tags, &note, &customerID); err != nil {
		t.Fatal(err)
	}
	if amount == nil || customerID == nil || note != nil || !placedAt.Equal(time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("complex values: amount=%v placed_at=%v tags=%v note=%v uuid=%v", amount, placedAt, tags, note, customerID)
	}
	tagsText := fmt.Sprint(tags)
	if !strings.Contains(tagsText, "priority") || !strings.Contains(tagsText, "east") {
		t.Fatalf("array round trip = %T(%v)", tags, tags)
	}
	if _, err := client.ExecContext(ctx, `FROM ridge.query(
		'INSERT INTO warehouse.sales.orders VALUES (3, NULL, 1, now(), NULL)')`); err == nil {
		t.Fatal("read-only attachment accepted write")
	}
	if output, err := exec.CommandContext(ctx, "docker", "stop", "-t", "1", container).CombinedOutput(); err != nil {
		t.Fatalf("stop PostgreSQL for disconnect test: %v: %s", err, output)
	}
	healthCtx, cancelHealth := context.WithTimeout(ctx, 5*time.Second)
	defer cancelHealth()
	if err := adapter.Health(healthCtx, definition); err == nil {
		t.Fatal("health check accepted a disconnected PostgreSQL source")
	}
}

func TestUnavailablePostgresDoesNotStopQuackIdentity(t *testing.T) {
	extensionDir := os.Getenv("QUACKRIDGE_EXTENSION_DIR")
	if extensionDir == "" {
		t.Skip("QUACKRIDGE_EXTENSION_DIR is required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	runtime := engine.New()
	endpoint, err := runtime.Start(ctx, quackridge.Options{ExtensionDir: extensionDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedPort := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	adapter := postgres.New(runtime, postgres.Config{
		Host: "127.0.0.1", Port: closedPort, Database: "missing", User: "reader", SSLMode: "disable",
	}, postgres.Credential{Password: "unusable-test-credential"})
	definition := source.Definition{ID: "unavailable", Name: "Unavailable", Alias: "unavailable", ConnectorType: "postgres", DatabaseType: "postgres", Enabled: true}
	if err := adapter.Attach(ctx, definition); err == nil {
		t.Fatal("unavailable PostgreSQL source unexpectedly attached")
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
	if _, err := client.ExecContext(ctx,
		fmt.Sprintf("ATTACH '%s' AS ridge (TYPE quack, TOKEN '%s')", endpoint, runtime.Token())); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := client.QueryRowContext(ctx, `SELECT name FROM ridge.query('FROM whoami()')`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "QuackRidge" {
		t.Fatalf("identity name = %q", name)
	}
	var sourceRows int
	if err := client.QueryRowContext(ctx,
		`SELECT count(*) FROM ridge.query('FROM quackridge_metadata_v2()')`).Scan(&sourceRows); err != nil {
		t.Fatal(err)
	}
	if sourceRows != 0 {
		t.Fatalf("unexpected healthy source rows = %d", sourceRows)
	}
}

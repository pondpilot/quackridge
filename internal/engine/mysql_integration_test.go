//go:build integration

package engine_test

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	quackridge "github.com/pondpilot/quackridge"
	"github.com/pondpilot/quackridge/internal/engine"
	"github.com/pondpilot/quackridge/internal/source"
	mysqlsource "github.com/pondpilot/quackridge/internal/source/mysql"
)

func TestMySQLMariaDBAttachAndMetadata(t *testing.T) {
	extensionDir := os.Getenv("QUACKRIDGE_EXTENSION_DIR")
	if extensionDir == "" {
		t.Skip("QUACKRIDGE_EXTENSION_DIR is required")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	container := "quackridge-mariadb-test-" + strconv.Itoa(os.Getpid())
	adminPassword, readerPassword := "temporary-admin-password", "temporary-reader-password"
	command := exec.CommandContext(ctx, "docker", "run", "--rm", "-d", "--name", container,
		"-e", "MARIADB_ROOT_PASSWORD="+adminPassword, "-e", "MARIADB_DATABASE=commerce",
		"-p", "127.0.0.1::3306", "mariadb:11.8")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start MariaDB: %v: %s", err, output)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "stop", "-t", "1", container).Run() })

	port := 0
	ready := false
	for ctx.Err() == nil {
		if output, err := exec.CommandContext(ctx, "docker", "port", container, "3306/tcp").Output(); err == nil {
			parts := strings.Split(strings.TrimSpace(string(output)), ":")
			port, _ = strconv.Atoi(parts[len(parts)-1])
		}
		if port > 0 {
			connection, dialErr := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 200*time.Millisecond)
			if dialErr == nil {
				_ = connection.Close()
				if exec.CommandContext(ctx, "docker", "exec", container, "mariadb", "-uroot", "-p"+adminPassword, "-e", "SELECT 1").Run() == nil {
					ready = true
					break
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !ready {
		t.Fatal("MariaDB did not become ready")
	}
	fixture := "CREATE TABLE commerce.orders(id INTEGER PRIMARY KEY, amount INTEGER); INSERT INTO commerce.orders VALUES (1,25),(2,40); CREATE VIEW commerce.large_orders AS SELECT * FROM commerce.orders WHERE amount > 30; CREATE USER 'qr_reader'@'%' IDENTIFIED BY '" + readerPassword + "'; GRANT SELECT ON commerce.* TO 'qr_reader'@'%'; FLUSH PRIVILEGES;"
	if output, err := exec.CommandContext(ctx, "docker", "exec", container, "mariadb", "-uroot", "-p"+adminPassword, "-e", fixture).CombinedOutput(); err != nil {
		t.Fatalf("create MariaDB fixture: %v: %s", err, output)
	}

	runtime := engine.New()
	if _, err := runtime.Start(ctx, quackridge.Options{ExtensionDir: extensionDir}); err != nil {
		t.Fatal(err)
	}
	defer runtime.Stop(context.Background())
	adapter := mysqlsource.New(runtime, mysqlsource.Config{Host: "127.0.0.1", Port: port, Database: "commerce", User: "qr_reader", SSLMode: "disabled"}, mysqlsource.Credential{Password: readerPassword})
	definition := source.Definition{ID: "commerce", Name: "Commerce", Alias: "commerce", ConnectorType: "mysql", Enabled: true}
	if err := adapter.Attach(ctx, definition); err != nil {
		t.Fatal(err)
	}
	var total int
	if err := runtime.QueryRow(ctx, "SELECT sum(amount)::INTEGER FROM commerce.commerce.orders").Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 65 {
		t.Fatalf("MariaDB total = %d", total)
	}
	var databaseType string
	if err := runtime.QueryRow(ctx, "SELECT database_type FROM quackridge_metadata_v2() WHERE source_id = ? LIMIT 1", definition.ID).Scan(&databaseType); err != nil {
		t.Fatal(err)
	}
	if databaseType != "mariadb" {
		t.Fatalf("database type = %q", databaseType)
	}
	var objectType string
	if err := runtime.QueryRow(ctx, `SELECT object_type FROM quackridge_metadata_v2()
		WHERE source_id = ? AND schema_name = 'commerce' AND object_name = 'large_orders' LIMIT 1`, definition.ID).Scan(&objectType); err != nil {
		t.Fatal(err)
	}
	if objectType != "view" {
		t.Fatalf("MariaDB view type = %q", objectType)
	}
}

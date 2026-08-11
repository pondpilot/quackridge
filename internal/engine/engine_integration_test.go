//go:build integration

package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	quackridge "github.com/pondpilot/quackridge"
)

func TestQuackIdentityAndShutdown(t *testing.T) {
	extensionDir := os.Getenv("QUACKRIDGE_EXTENSION_DIR")
	if extensionDir == "" {
		t.Skip("QUACKRIDGE_EXTENSION_DIR is required")
	}
	runtime := New()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	endpoint, err := runtime.Start(ctx, quackridge.Options{ExtensionDir: extensionDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Stop(context.Background()); err != nil {
			t.Errorf("stop: %v", err)
		}
	})

	client, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, extension := range []string{"httpfs", "quack"} {
		if err := loadExtension(ctx, client, extensionDir+"/"+extension+".duckdb_extension"); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = runtime.db.ExecContext(ctx, "CALL enable_logging('Quack')")
	attach := "ATTACH '" + strings.ReplaceAll(endpoint, "'", "''") + "' AS ridge (TYPE quack, TOKEN '" + runtime.Token() + "')"
	if _, err := client.ExecContext(ctx, attach); err != nil {
		rows, _ := runtime.db.QueryContext(ctx, `SELECT query, error FROM duckdb_logs_parsed('Quack')
			WHERE message_type = 'PREPARE_REQUEST' ORDER BY timestamp`)
		defer rows.Close()
		var decisions []string
		for rows.Next() {
			var query, serverError sql.NullString
			_ = rows.Scan(&query, &serverError)
			decisions = append(decisions, query.String+" => "+serverError.String)
		}
		t.Fatalf("%v; server decisions=%q", err, decisions)
	}
	var name string
	var metaValue any
	if err := client.QueryRowContext(ctx, "SELECT name, meta FROM ridge.query('FROM whoami()')").Scan(&name, &metaValue); err != nil {
		var serverQuery, serverError sql.NullString
		_ = runtime.db.QueryRowContext(ctx, `SELECT query, error FROM duckdb_logs_parsed('Quack')
			WHERE message_type = 'PREPARE_REQUEST' ORDER BY timestamp DESC LIMIT 1`).Scan(&serverQuery, &serverError)
		t.Fatalf("%v; server query=%q error=%q", err, serverQuery.String, serverError.String)
	}
	metaBytes, err := json.Marshal(metaValue)
	if err != nil {
		t.Fatal(err)
	}
	meta := string(metaBytes)
	if name != "QuackRidge" || !strings.Contains(meta, `"protocol_version":1`) {
		t.Fatalf("identity name=%q meta=%q", name, meta)
	}
	var identity struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(metaBytes, &identity); err != nil {
		t.Fatalf("decode identity metadata: %v", err)
	}
	if !contains(identity.Capabilities, "cancellation_noop") || contains(identity.Capabilities, "cancel") {
		t.Fatalf("identity does not advertise the cancellation waiver honestly: %v", identity.Capabilities)
	}
	if err := runtime.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	address := strings.TrimPrefix(endpoint, "quack:")
	connection, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
	if err == nil {
		_ = connection.Close()
		t.Fatalf("Quack port %s remained open after Stop", address)
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestQuackCancellationIsExplicitlyNoop(t *testing.T) {
	extensionDir := os.Getenv("QUACKRIDGE_EXTENSION_DIR")
	if extensionDir == "" {
		t.Skip("QUACKRIDGE_EXTENSION_DIR is required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, extension := range []string{"httpfs", "quack"} {
		if err := loadExtension(ctx, client, extensionDir+"/"+extension+".duckdb_extension"); err != nil {
			t.Fatal(err)
		}
	}
	var functions int
	if err := client.QueryRowContext(ctx,
		`SELECT count(*) FROM duckdb_functions() WHERE function_name = 'quack_cancel'`).Scan(&functions); err != nil {
		t.Fatal(err)
	}
	if functions != 0 {
		t.Fatalf("pinned Quack unexpectedly exposes cancellation; revisit the no-op capability")
	}
	for _, capability := range quackridge.Capabilities() {
		if capability == "cancel" {
			t.Fatal("unsupported cancellation is advertised")
		}
	}
}

func TestEngineSandboxAndLimits(t *testing.T) {
	extensionDir := os.Getenv("QUACKRIDGE_EXTENSION_DIR")
	if extensionDir == "" {
		t.Skip("QUACKRIDGE_EXTENSION_DIR is required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	runtime := New()
	endpoint, err := runtime.Start(ctx, quackridge.Options{
		ExtensionDir: extensionDir,
		MemoryLimit:  "64MB",
		TempLimit:    "16MB",
		Threads:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })

	settings := map[string]string{}
	rows, err := runtime.db.QueryContext(ctx, `SELECT name, value FROM duckdb_settings()
		WHERE name IN ('autoinstall_known_extensions', 'autoload_known_extensions',
		'allow_community_extensions', 'allow_unsigned_extensions', 'allow_unredacted_secrets',
		'enable_global_s3_configuration', 'lock_configuration', 'threads', 'memory_limit',
		'max_temp_directory_size', 'default_secret_storage')`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			t.Fatal(err)
		}
		settings[name] = value
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"autoinstall_known_extensions", "autoload_known_extensions", "allow_community_extensions", "allow_unsigned_extensions", "allow_unredacted_secrets", "enable_global_s3_configuration"} {
		if settings[name] != "false" {
			t.Fatalf("%s = %q, want false", name, settings[name])
		}
	}
	if settings["lock_configuration"] != "true" || settings["threads"] != "1" || settings["default_secret_storage"] != "memory" {
		t.Fatalf("locked settings = %v", settings)
	}
	if settings["memory_limit"] == "" || settings["max_temp_directory_size"] == "" {
		t.Fatalf("resource settings are absent: %v", settings)
	}
	if _, err := runtime.db.ExecContext(ctx, "SET threads = 8"); err == nil {
		t.Fatal("locked engine accepted a configuration change")
	}
	if _, err := runtime.db.ExecContext(ctx, "SELECT * FROM read_text('/etc/passwd')"); err == nil {
		t.Fatal("engine read a file outside its sandbox")
	}
	if _, err := runtime.db.ExecContext(ctx, "INSTALL json"); err == nil {
		t.Fatal("locked engine installed an extension")
	}

	client, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, extension := range []string{"httpfs", "quack"} {
		if err := loadExtension(ctx, client, extensionDir+"/"+extension+".duckdb_extension"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.ExecContext(ctx,
		"ATTACH '"+endpoint+"' AS ridge (TYPE quack, TOKEN '"+runtime.Token()+"')"); err != nil {
		t.Fatal(err)
	}
	denied := []string{
		"CREATE TABLE blocked(value INTEGER)",
		"CREATE PERSISTENT SECRET blocked (TYPE postgres, HOST '127.0.0.1')",
		"SET memory_limit = '2GB'",
		"LOAD json",
		"SELECT * FROM read_text('/etc/passwd')",
	}
	for _, query := range denied {
		wrapped := "FROM ridge.query('" + strings.ReplaceAll(query, "'", "''") + "')"
		if _, err := client.ExecContext(ctx, wrapped); err == nil {
			t.Fatalf("authorization accepted %q", query)
		}
	}

	var oversized any
	resourceErr := runtime.db.QueryRowContext(ctx, "SELECT list(i) FROM range(10000000) t(i)").Scan(&oversized)
	if resourceErr == nil || !quackridge.IsCode(quackridge.ClassifyError(resourceErr), quackridge.CodeResourceExhausted) {
		t.Fatalf("memory limit error = %v", resourceErr)
	}
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, time.Nanosecond)
	defer timeoutCancel()
	time.Sleep(time.Millisecond)
	var total uint64
	timeoutErr := runtime.db.QueryRowContext(timeoutCtx, "SELECT sum(i)::UBIGINT FROM range(1000000000) t(i)").Scan(&total)
	if timeoutErr == nil || !quackridge.IsCode(quackridge.ClassifyError(errors.Join(timeoutErr, timeoutCtx.Err())), quackridge.CodeTimeout) {
		t.Fatalf("timeout error = %v", timeoutErr)
	}
	sandbox := runtime.sandbox
	if err := runtime.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sandbox); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sandbox remained after shutdown: %v", err)
	}
}

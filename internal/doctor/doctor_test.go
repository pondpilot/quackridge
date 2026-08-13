package doctor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	quackridge "github.com/pondpilot/quackridge"
	"github.com/pondpilot/quackridge/internal/config"
	"github.com/pondpilot/quackridge/internal/secrets"
)

func TestRunVerifiesConfigurationCredentialsAndExtensions(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.json")
	document := config.Document{Version: config.CurrentVersion, Sources: []config.Source{{
		ID: "warehouse", Name: "Warehouse", Alias: "warehouse", Type: "postgres", Enabled: true,
		CredentialRef: "quackridge/source/warehouse", Options: []byte(`{"host":"localhost"}`),
	}}}
	if err := (config.Store{Path: configPath}).Save(document); err != nil {
		t.Fatal(err)
	}
	extensionDir := filepath.Join(directory, "extensions")
	if err := os.Mkdir(extensionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var checksums string
	for _, name := range []string{"httpfs.duckdb_extension", "mysql_scanner.duckdb_extension", "odbc_scanner.duckdb_extension", "postgres_scanner.duckdb_extension", "quack.duckdb_extension", "sqlite_scanner.duckdb_extension"} {
		contents := []byte("test-" + name)
		if err := os.WriteFile(filepath.Join(extensionDir, name), contents, 0o600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(contents)
		checksums += fmt.Sprintf("%s  %s\n", hex.EncodeToString(digest[:]), name)
	}
	versions := []byte("duckdb 1.5.5\nhttpfs 827222f\nmysql_scanner 7267164\nodbc_scanner 274a330\npostgres_scanner 41223e5\nquack c154811\nsqlite_scanner f79b1db\n")
	if err := os.WriteFile(filepath.Join(extensionDir, "extensions.versions"), versions, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(versions)
	checksums += fmt.Sprintf("%s  extensions.versions\n", hex.EncodeToString(digest[:]))
	if err := os.WriteFile(filepath.Join(extensionDir, "extensions.sha256"), []byte(checksums), 0o600); err != nil {
		t.Fatal(err)
	}
	credentials := secrets.NewMemory()
	if err := credentials.Put(context.Background(), "quackridge/source/warehouse", []byte("not-reported")); err != nil {
		t.Fatal(err)
	}
	report := Run(context.Background(), Options{
		ConfigPath: configPath, ControlAddress: filepath.Join(directory, "missing.sock"),
		ExtensionDir: extensionDir, CredentialStore: credentials,
	})
	if !report.OK {
		t.Fatalf("report = %#v", report)
	}
	for _, check := range report.Checks {
		if check.Level == LevelError {
			t.Fatalf("unexpected failed check: %#v", check)
		}
	}
}

func TestSourceDiagnosticFailuresAffectReportLevels(t *testing.T) {
	report := Report{OK: true}
	addSourceDiagnosticChecks(&report, []any{map[string]any{
		"id": "warehouse", "health": "unavailable",
		"warnings": []any{"source connectivity check failed", "role can create databases"},
	}})
	if len(report.Checks) != 3 || report.Checks[0].Level != LevelError || report.Checks[1].Level != LevelWarning {
		t.Fatalf("checks = %#v", report.Checks)
	}
}

func TestExtensionVersionPairMustMatchBuild(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "extensions.versions")
	versions := "duckdb 9.9.9\n"
	for name, version := range quackridge.ExtensionVersions() {
		versions += name + " " + version + "\n"
	}
	if err := os.WriteFile(path, []byte(versions), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkExtensionVersions(path); err == nil {
		t.Fatal("mismatched DuckDB version accepted")
	}
}

func TestRunFailsClosedOnExtensionDriftAndMissingCredential(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.json")
	document := config.Document{Version: config.CurrentVersion, Sources: []config.Source{{
		ID: "warehouse", Name: "Warehouse", Alias: "warehouse", Type: "postgres", Enabled: true,
		CredentialRef: "quackridge/source/warehouse", Options: []byte(`{"host":"localhost"}`),
	}}}
	if err := (config.Store{Path: configPath}).Save(document); err != nil {
		t.Fatal(err)
	}
	extensionDir := filepath.Join(directory, "extensions")
	if err := os.Mkdir(extensionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extensionDir, "extensions.sha256"), []byte(
		"0000000000000000000000000000000000000000000000000000000000000000  httpfs.duckdb_extension\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report := Run(context.Background(), Options{
		ConfigPath: configPath, ControlAddress: filepath.Join(directory, "missing.sock"),
		ExtensionDir: extensionDir, CredentialStore: secrets.NewMemory(),
	})
	if report.OK {
		t.Fatalf("report unexpectedly passed: %#v", report)
	}
	var errorsFound int
	for _, check := range report.Checks {
		if check.Level == LevelError {
			errorsFound++
		}
	}
	if errorsFound != 2 {
		t.Fatalf("error checks = %d, report = %#v", errorsFound, report)
	}
}

func TestLoopbackEndpoint(t *testing.T) {
	for _, endpoint := range []string{"quack:127.0.0.1:5432", "quack:[::1]:5432"} {
		if !loopbackEndpoint(endpoint) {
			t.Fatalf("expected loopback endpoint: %s", endpoint)
		}
	}
	if loopbackEndpoint("quack:0.0.0.0:5432") {
		t.Fatal("wildcard endpoint accepted")
	}
}

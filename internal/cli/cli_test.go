package cli

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestVersionJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	application := &App{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr}
	if code := application.Run([]string{"version", "--json"}); code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
	}
	var response map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["product"] != "quackridge" {
		t.Fatalf("version = %#v", response)
	}
}

func TestCredentiallessFileSourceDoesNotRequireSystemSecretStore(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "support.sqlite")
	if err := os.WriteFile(databasePath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "config.json")
	var stdout, stderr bytes.Buffer
	application := &App{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr}
	code := application.Run([]string{
		"source", "add", "sqlite", "--config", configPath,
		"--id", "support", "--name", "Support", "--alias", "support", "--path", databasePath,
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
	}
	document, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(document, []byte(`"credential_ref": ""`)) {
		t.Fatalf("config = %s", document)
	}
}

func TestODBCUserRequiresPasswordInput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	application := &App{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr}
	code := application.Run([]string{
		"source", "test", "odbc", "--id", "support", "--name", "Support", "--alias", "support",
		"--dsn", "support", "--database-type", "sqlserver", "--user", "reader",
	})
	if code == 0 || !strings.Contains(stderr.String(), "use --password-stdin") {
		t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
	}
}

func TestSourceTestReadsPasswordFromStdinWithoutPersistence(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	configPath := filepath.Join(t.TempDir(), "config.json")
	password := "sensitive-test-password"
	var stdout, stderr bytes.Buffer
	application := &App{Stdin: strings.NewReader(password + "\n"), Stdout: &stdout, Stderr: &stderr}
	code := application.Run([]string{
		"source", "test", "postgres", "--config", configPath,
		"--id", "warehouse", "--name", "Warehouse", "--alias", "warehouse",
		"--host", "127.0.0.1", "--port", strconv.Itoa(port), "--database", "analytics",
		"--user", "reader", "--ssl-mode", "disable", "--password-stdin", "--json",
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), password) {
		t.Fatal("CLI output leaked the password")
	}
	if !strings.Contains(stdout.String(), `"persisted":false`) {
		t.Fatalf("output = %s", stdout.String())
	}
	if listed := application.Run([]string{"source", "list", "--config", configPath, "--json"}); listed != 0 {
		t.Fatalf("list exit = %d", listed)
	}
}

func TestPasswordFlagIsNotAcceptedOrEchoed(t *testing.T) {
	password := "must-not-enter-arguments"
	var stdout, stderr bytes.Buffer
	application := &App{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr}
	code := application.Run([]string{"source", "test", "postgres", "--password", password})
	if code == 0 {
		t.Fatal("password argument was accepted")
	}
	if strings.Contains(stdout.String()+stderr.String(), password) {
		t.Fatal("rejected password argument was echoed")
	}
}

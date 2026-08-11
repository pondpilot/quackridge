package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func testDocument() Document {
	return Document{Version: CurrentVersion, Sources: []Source{{
		ID: "warehouse", Name: "Warehouse", Alias: "warehouse", Type: "postgres", Enabled: true,
		CredentialRef: "quackridge/source/warehouse",
		Options:       json.RawMessage(`{"host":"127.0.0.1","port":5432,"database":"analytics","user":"reader","ssl_mode":"require"}`),
	}}}
}

func TestStoreRoundTripPermissionsBackupAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	store := Store{Path: path}
	document := testDocument()
	if err := store.Save(document); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions = %o", info.Mode().Perm())
	}
	loaded, err := store.Load()
	if err != nil || loaded.Sources[0].Name != "Warehouse" {
		t.Fatalf("load = %#v, %v", loaded, err)
	}

	document.Sources[0].Name = "Updated"
	if err := store.Save(document); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"sources":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Sources[0].Name != "Warehouse" {
		t.Fatalf("recovered = %#v", recovered)
	}
}

func TestConfigRejectsDuplicatesAndSecrets(t *testing.T) {
	document := testDocument()
	document.Sources = append(document.Sources, document.Sources[0])
	if err := document.Validate(); err == nil {
		t.Fatal("duplicate source was accepted")
	}
	document = testDocument()
	document.Sources[0].Options = json.RawMessage(`{"host":"localhost","nested":{"password":"leak"}}`)
	if err := document.Validate(); err == nil {
		t.Fatal("secret-bearing options were accepted")
	}
}

func TestLoadMissingReturnsEmptyCurrentDocument(t *testing.T) {
	loaded, err := (Store{Path: filepath.Join(t.TempDir(), "missing.json")}).Load()
	if err != nil || loaded.Version != CurrentVersion || len(loaded.Sources) != 0 {
		t.Fatalf("missing load = %#v, %v", loaded, err)
	}
}

func TestLoadMigratesVersionZeroWithBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	legacy := testDocument()
	legacy.Version = 0
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := (Store{Path: path}).Load()
	if err != nil || loaded.Version != CurrentVersion {
		t.Fatalf("migrated = %#v, %v", loaded, err)
	}
	backup, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	var backedUp Document
	if err := json.Unmarshal(backup, &backedUp); err != nil || backedUp.Version != 0 {
		t.Fatalf("migration backup = %#v, %v", backedUp, err)
	}
}

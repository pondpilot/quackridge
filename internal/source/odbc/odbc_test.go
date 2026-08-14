package odbc

import (
	"strings"
	"testing"

	"github.com/pondpilot/quackridge/internal/source"
)

func TestValidateAndBuildCredentialFreeConnectionString(t *testing.T) {
	adapter := New(nil, Config{
		Driver: "ODBC Driver 18 for SQL Server", DatabaseType: "sqlserver",
		Properties: map[string]string{"Server": "localhost", "Database": "support"},
	}, Credential{Username: "reader", Password: "not-in-config"})
	definition := source.Definition{ID: "support", Name: "Support", Alias: "support", ConnectorType: "odbc", DatabaseType: "sqlserver", Enabled: true}
	if err := adapter.Validate(t.Context(), definition); err != nil {
		t.Fatal(err)
	}
	connection := adapter.connectionString()
	if strings.Contains(connection, "reader") || strings.Contains(connection, "not-in-config") {
		t.Fatal("credential leaked into persisted connection string")
	}
	if connection != "Driver={ODBC Driver 18 for SQL Server};Database=support;Server=localhost" {
		t.Fatalf("connection = %q", connection)
	}
}

func TestConnectionValuesAreBracedOnlyWhenRequired(t *testing.T) {
	for value, expected := range map[string]string{
		"/tmp/support.sqlite": "/tmp/support.sqlite",
		"with;separator":      "{with;separator}",
		"closing}brace":       "{closing}}brace}",
		" padded ":            "{ padded }",
	} {
		if got := connectionValue(value); got != expected {
			t.Fatalf("connectionValue(%q) = %q, want %q", value, got, expected)
		}
	}
}

func TestRejectsAmbiguousAndSecretBearingProperties(t *testing.T) {
	definition := source.Definition{ID: "support", Name: "Support", Alias: "support", ConnectorType: "odbc", DatabaseType: "sqlserver", Enabled: true}
	for name, config := range map[string]Config{
		"dsn and driver":           {DSN: "support", Driver: "driver", DatabaseType: "sqlserver"},
		"connection string as dsn": {DSN: "Server=db;UID=reader;PWD=plaintext", DatabaseType: "sqlserver"},
		"secret property":          {DSN: "support", DatabaseType: "sqlserver", Properties: map[string]string{"AccessToken": "secret"}},
		"padded secret property":   {DSN: "support", DatabaseType: "sqlserver", Properties: map[string]string{"PWD ": "secret"}},
		"unknown database":         {DSN: "support", DatabaseType: "unknown"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := New(nil, config, Credential{}).Validate(t.Context(), definition); err == nil {
				t.Fatal("invalid ODBC configuration accepted")
			}
		})
	}
}

func TestSecureUnknownPropertiesStayInCredential(t *testing.T) {
	adapter := New(nil, Config{DSN: "support", DatabaseType: "sqlserver", Properties: map[string]string{"Encrypt": "yes"}},
		Credential{SecureProperties: map[string]string{"AccessToken": "synthetic-secret", "VendorBlob": "opaque"}})
	definition := source.Definition{ID: "support", Name: "Support", Alias: "support", ConnectorType: "odbc", DatabaseType: "sqlserver", Enabled: true}
	if err := adapter.Validate(t.Context(), definition); err != nil {
		t.Fatal(err)
	}
	connection := adapter.connectionString()
	if !strings.Contains(connection, "AccessToken=synthetic-secret") || !strings.Contains(connection, "VendorBlob=opaque") {
		t.Fatalf("connection = %q", connection)
	}
	if PublicPropertyAllowed("sqlserver", "AccessToken") {
		t.Fatal("credential property was allowlisted as public")
	}
}

func TestCredentialRejectsTrailingJSONAndCaseVariantProperties(t *testing.T) {
	if _, err := DecodeCredential([]byte(`{"username":"reader"} {"password":"ignored"}`)); err == nil {
		t.Fatal("trailing credential JSON was accepted")
	}
	adapter := New(nil, Config{DSN: "support", DatabaseType: "sqlserver", Properties: map[string]string{"Encrypt": "yes"}},
		Credential{SecureProperties: map[string]string{"ENCRYPT": "no"}})
	definition := source.Definition{ID: "support", Name: "Support", Alias: "support", ConnectorType: "odbc", DatabaseType: "sqlserver", Enabled: true}
	if err := adapter.Validate(t.Context(), definition); err == nil {
		t.Fatal("case-variant public and secure properties were accepted")
	}
}

func TestReservedSchemasAreNotMaterialized(t *testing.T) {
	tests := map[string][]string{
		"postgres":  {"information_schema", "pg_catalog"},
		"mysql":     {"information_schema", "mysql", "performance_schema", "sys"},
		"mariadb":   {"information_schema", "mysql", "performance_schema", "sys"},
		"sqlserver": {"information_schema", "sys"},
		"oracle":    {"SYS", "SYSTEM", "OUTLN", "XDB"},
	}
	for databaseType, schemas := range tests {
		for _, schema := range schemas {
			if !reservedSchema(databaseType, schema) {
				t.Fatalf("%s schema %q was not reserved", databaseType, schema)
			}
		}
		if reservedSchema(databaseType, "application") {
			t.Fatalf("%s application schema was reserved", databaseType)
		}
	}
}

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
	if connection != "Driver={ODBC Driver 18 for SQL Server};Database={support};Server={localhost}" {
		t.Fatalf("connection = %q", connection)
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

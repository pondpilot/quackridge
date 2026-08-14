package odbc

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/pondpilot/quackridge/internal/engine"
	"github.com/pondpilot/quackridge/internal/odbcprops"
	"github.com/pondpilot/quackridge/internal/source"
)

type Config struct {
	DSN          string            `json:"dsn,omitempty"`
	Driver       string            `json:"driver,omitempty"`
	Properties   map[string]string `json:"properties,omitempty"`
	DatabaseType string            `json:"database_type"`
}

type Credential struct {
	Username         string            `json:"username,omitempty"`
	Password         string            `json:"password,omitempty"`
	SecureProperties map[string]string `json:"secure_properties,omitempty"`
}

func DecodeCredential(raw []byte) (Credential, error) {
	if len(raw) == 0 {
		return Credential{}, nil
	}
	var credential Credential
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&credential); err != nil {
		return Credential{}, fmt.Errorf("ODBC credential must contain username and password JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Credential{}, fmt.Errorf("ODBC credential must contain one JSON object")
	}
	return credential, nil
}

type Attacher interface {
	AttachODBC(context.Context, engine.ODBCAttachment) error
	ODBCQuery(context.Context, string, string, string, string, string) (*sql.Rows, error)
	ClearODBC(string)
	Detach(context.Context, string, string) error
	Query(context.Context, string, ...any) (*sql.Rows, error)
}

type Adapter struct {
	engine     Attacher
	config     Config
	credential Credential
}

func New(attacher Attacher, config Config, credential Credential) *Adapter {
	return &Adapter{engine: attacher, config: config, credential: credential}
}

func (*Adapter) Type() string { return "odbc" }

func (a *Adapter) Validate(_ context.Context, definition source.Definition) error {
	if definition.ID == "" || definition.Name == "" {
		return fmt.Errorf("source id and name are required")
	}
	if err := source.ValidateAlias(definition.Alias); err != nil {
		return err
	}
	if (a.config.DSN == "") == (a.config.Driver == "") {
		return fmt.Errorf("exactly one of dsn or driver is required")
	}
	if a.config.DSN != "" && !validDSN(a.config.DSN) {
		return fmt.Errorf("ODBC dsn must be a non-secret data source name")
	}
	switch a.databaseType() {
	case "odbc", "postgres", "mysql", "mariadb", "sqlite", "duckdb", "sqlserver", "oracle":
	default:
		return fmt.Errorf("ODBC database_type is unsupported")
	}
	propertyNames := make(map[string]string, len(a.config.Properties)+len(a.credential.SecureProperties))
	for key := range a.config.Properties {
		if !validPropertyKey(key) {
			return fmt.Errorf("ODBC property is invalid")
		}
		if !PublicPropertyAllowed(a.databaseType(), key) {
			return fmt.Errorf("ODBC property must use the secure credential input")
		}
		normalized := strings.ToUpper(strings.TrimSpace(key))
		if _, exists := propertyNames[normalized]; exists {
			return fmt.Errorf("ODBC property names must be unique ignoring case")
		}
		propertyNames[normalized] = "public"
	}
	for key := range a.credential.SecureProperties {
		if !validPropertyKey(key) {
			return fmt.Errorf("ODBC secure property is invalid")
		}
		normalized := strings.ToUpper(strings.TrimSpace(key))
		if kind, exists := propertyNames[normalized]; exists && kind == "public" {
			return fmt.Errorf("ODBC property cannot be both public and secure")
		}
		if _, exists := propertyNames[normalized]; exists {
			return fmt.Errorf("ODBC secure property names must be unique ignoring case")
		}
		propertyNames[normalized] = "secure"
	}
	return nil
}

func validDSN(value string) bool {
	return value == strings.TrimSpace(value) && value != "" && !strings.ContainsAny(value, ";={}\r\n")
}

func validPropertyKey(value string) bool {
	if value == "" || value != strings.TrimSpace(value) {
		return false
	}
	for _, character := range value {
		if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func PublicPropertyAllowed(databaseType, key string) bool {
	return odbcprops.PublicAllowed(databaseType, key)
}

func (a *Adapter) Attach(ctx context.Context, definition source.Definition) error {
	if err := a.Validate(ctx, definition); err != nil {
		return err
	}
	objects, err := a.discover(ctx, definition.ID)
	if err != nil {
		return err
	}
	return a.engine.AttachODBC(ctx, engine.ODBCAttachment{
		SourceID: definition.ID, SourceName: definition.Name, Alias: definition.Alias,
		DatabaseType: a.databaseType(), Connection: a.connectionString(),
		Username: a.credential.Username, Password: a.credential.Password, Objects: objects,
	})
}

func (a *Adapter) discover(ctx context.Context, sourceID string) ([]engine.ODBCObject, error) {
	probeID, err := newProbeID()
	if err != nil {
		return nil, fmt.Errorf("create ODBC metadata probe: %w", err)
	}
	defer a.engine.ClearODBC(probeID)
	query := "SELECT table_schema, table_name, CASE WHEN table_type = 'VIEW' THEN 'view' ELSE 'table' END object_type FROM information_schema.tables"
	if a.databaseType() == "sqlite" {
		query = "SELECT 'main' table_schema, name table_name, CASE WHEN type = 'view' THEN 'view' ELSE 'table' END object_type FROM sqlite_master WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%'"
	} else if a.databaseType() == "oracle" {
		query = "SELECT owner table_schema, table_name, 'table' object_type FROM all_tables UNION ALL SELECT owner, view_name table_name, 'view' object_type FROM all_views"
	}
	rows, err := a.engine.ODBCQuery(ctx, probeID, a.connectionString(), a.credential.Username, a.credential.Password, query)
	if err != nil {
		return nil, fmt.Errorf("ODBC metadata discovery failed: %w", err)
	}
	defer rows.Close()
	var objects []engine.ODBCObject
	for rows.Next() {
		var object engine.ODBCObject
		if err := rows.Scan(&object.Schema, &object.Name, &object.Type); err != nil {
			return nil, fmt.Errorf("ODBC metadata discovery failed: %w", err)
		}
		if reservedSchema(a.databaseType(), object.Schema) {
			continue
		}
		object.RemoteSelect = "SELECT * FROM " + a.remoteName(object.Schema, object.Name)
		objects = append(objects, object)
	}
	return objects, rows.Err()
}

func reservedSchema(databaseType, schema string) bool {
	normalized := strings.ToLower(schema)
	switch databaseType {
	case "postgres", "duckdb":
		return normalized == "information_schema" || normalized == "pg_catalog"
	case "mysql", "mariadb":
		return normalized == "information_schema" || normalized == "mysql" || normalized == "performance_schema" || normalized == "sys"
	case "sqlserver":
		return normalized == "information_schema" || normalized == "sys"
	case "oracle":
		return normalized == "sys" || normalized == "system" || normalized == "outln" || normalized == "xdb"
	default:
		return normalized == "information_schema"
	}
}

func (a *Adapter) remoteName(schema, name string) string {
	quote := `"`
	if a.databaseType() == "mysql" || a.databaseType() == "mariadb" {
		quote = "`"
	}
	escape := func(value string) string { return quote + strings.ReplaceAll(value, quote, quote+quote) + quote }
	if a.databaseType() == "sqlite" {
		return escape(name)
	}
	return escape(schema) + "." + escape(name)
}

func (a *Adapter) connectionString() string {
	properties := make(map[string]string, len(a.config.Properties)+len(a.credential.SecureProperties))
	for key, value := range a.config.Properties {
		properties[key] = value
	}
	for key, value := range a.credential.SecureProperties {
		properties[key] = value
	}
	parts := make([]string, 0, len(properties)+1)
	if a.config.DSN != "" {
		parts = append(parts, "DSN={"+escape(a.config.DSN)+"}")
	} else {
		parts = append(parts, "Driver={"+escape(a.config.Driver)+"}")
	}
	keys := make([]string, 0, len(properties))
	for key := range properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, key+"="+connectionValue(properties[key]))
	}
	return strings.Join(parts, ";")
}

func escape(value string) string { return strings.ReplaceAll(value, "}", "}}") }

func connectionValue(value string) string {
	if value == "" || value != strings.TrimSpace(value) || strings.ContainsAny(value, ";{}") {
		return "{" + escape(value) + "}"
	}
	return value
}

func (a *Adapter) databaseType() string {
	if a.config.DatabaseType == "" {
		return "odbc"
	}
	return a.config.DatabaseType
}

func (a *Adapter) Metadata(ctx context.Context, definition source.Definition) ([]source.MetadataRow, error) {
	return source.ReadMetadata(ctx, a.engine, definition.ID)
}

func (a *Adapter) Health(ctx context.Context, definition source.Definition) error {
	probeID, err := newProbeID()
	if err != nil {
		return fmt.Errorf("ODBC health check failed")
	}
	defer a.engine.ClearODBC(probeID)
	rows, err := a.engine.ODBCQuery(ctx, probeID, a.connectionString(), a.credential.Username, a.credential.Password, "SELECT 1")
	if err != nil {
		return fmt.Errorf("ODBC health check failed")
	}
	return rows.Close()
}

func newProbeID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "probe_" + hex.EncodeToString(value[:]), nil
}

func (a *Adapter) Cleanup(ctx context.Context, definition source.Definition) error {
	return a.engine.Detach(ctx, definition.Alias, definition.ID)
}

var _ source.Adapter = (*Adapter)(nil)

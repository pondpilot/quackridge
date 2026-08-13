package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/pondpilot/quackridge/internal/engine"
	"github.com/pondpilot/quackridge/internal/source"
)

type Config struct {
	Host        string            `json:"host"`
	Port        int               `json:"port"`
	Database    string            `json:"database"`
	User        string            `json:"user"`
	SSLMode     string            `json:"ssl_mode"`
	RootCertRef string            `json:"root_certificate_ref,omitempty"`
	Options     map[string]string `json:"options,omitempty"`
}

type Credential struct {
	Password            string
	RootCertificatePath string
}

type Attacher interface {
	Attach(context.Context, engine.Attachment) error
	Detach(context.Context, string, string) error
	Query(context.Context, string, ...any) (*sql.Rows, error)
	QueryRow(context.Context, string, ...any) *sql.Row
	RegisterObjectTypes(context.Context, string, []engine.ObjectType) error
}

type Adapter struct {
	engine     Attacher
	config     Config
	credential Credential
	timeout    time.Duration
}

func New(attacher Attacher, config Config, credential Credential) *Adapter {
	return &Adapter{engine: attacher, config: config, credential: credential, timeout: 10 * time.Second}
}

func (a *Adapter) Type() string { return "postgres" }

func (a *Adapter) Validate(ctx context.Context, definition source.Definition) error {
	if definition.ID == "" || definition.Name == "" {
		return fmt.Errorf("source id and name are required")
	}
	if err := source.ValidateAlias(definition.Alias); err != nil {
		return err
	}
	if a.config.Host == "" || a.config.Database == "" || a.config.User == "" {
		return fmt.Errorf("host, database, and user are required")
	}
	if a.config.Port < 1 || a.config.Port > 65535 {
		return fmt.Errorf("invalid PostgreSQL port")
	}
	if !validSSLMode(a.config.SSLMode) {
		return fmt.Errorf("invalid PostgreSQL SSL mode")
	}
	if a.config.SSLMode == "verify-ca" || a.config.SSLMode == "verify-full" {
		if a.config.RootCertRef == "" || a.credential.RootCertificatePath == "" {
			return fmt.Errorf("root certificate reference is required for %s", a.config.SSLMode)
		}
	}
	if a.credential.Password == "" {
		return fmt.Errorf("PostgreSQL credential is unavailable")
	}
	dialer := net.Dialer{Timeout: a.timeout}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(a.config.Host, strconv.Itoa(a.config.Port)))
	if err != nil {
		return fmt.Errorf("PostgreSQL connectivity check failed: %w", err)
	}
	return connection.Close()
}

func (a *Adapter) Attach(ctx context.Context, definition source.Definition) error {
	if err := a.Validate(ctx, definition); err != nil {
		return err
	}
	if err := a.engine.Attach(ctx, engine.Attachment{
		SourceID: definition.ID, SourceName: definition.Name, Alias: definition.Alias,
		Type: "postgres", DatabaseType: "postgres", Secret: a.secretValues(), ReadOnly: true,
	}); err != nil {
		return err
	}
	if err := a.verifyReadOnly(ctx, definition.Alias); err != nil {
		_ = a.engine.Detach(context.Background(), definition.Alias, definition.ID)
		return err
	}
	return nil
}

func (a *Adapter) verifyReadOnly(ctx context.Context, alias string) error {
	quotedAlias := strings.ReplaceAll(alias, "'", "''")
	var setting string
	if err := a.engine.QueryRow(ctx,
		"SELECT setting FROM postgres_query('"+quotedAlias+"', 'SELECT current_setting(''transaction_read_only'') AS setting')").Scan(&setting); err != nil {
		return fmt.Errorf("verify PostgreSQL read-only transaction: %w", err)
	}
	if setting != "on" {
		return errors.New("PostgreSQL role does not default to read-only transactions")
	}
	return nil
}

func (a *Adapter) registerObjectTypes(ctx context.Context, definition source.Definition) error {
	quotedAlias := strings.ReplaceAll(definition.Alias, "'", "''")
	rows, err := a.engine.Query(ctx, "SELECT table_schema, table_name, object_type FROM postgres_query('"+quotedAlias+"', "+
		"'SELECT table_schema, table_name, CASE WHEN table_type = ''VIEW'' THEN ''view'' ELSE ''table'' END AS object_type FROM information_schema.tables')")
	if err != nil {
		return fmt.Errorf("load PostgreSQL object metadata: %w", err)
	}
	defer rows.Close()
	var objects []engine.ObjectType
	for rows.Next() {
		var object engine.ObjectType
		if err := rows.Scan(&object.Schema, &object.Name, &object.Type); err != nil {
			return err
		}
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return a.engine.RegisterObjectTypes(ctx, definition.ID, objects)
}

func (a *Adapter) Metadata(ctx context.Context, definition source.Definition) ([]source.MetadataRow, error) {
	rows, err := a.engine.Query(ctx, `SELECT source_id, source_name, connector_type, database_type, source_health,
		catalog_name, schema_name, object_name, object_type, column_name, ordinal_position,
		duckdb_type, nullable, is_system_schema, error_code FROM quackridge_metadata_v2() WHERE source_id = ?
		ORDER BY schema_name, object_name, ordinal_position`, definition.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var metadata []source.MetadataRow
	for rows.Next() {
		var row source.MetadataRow
		var schemaName, objectName, objectType, columnName, duckDBType, errorCode sql.NullString
		var ordinal sql.NullInt64
		var nullable, isSystemSchema sql.NullBool
		if err := rows.Scan(&row.SourceID, &row.SourceName, &row.ConnectorType, &row.DatabaseType, &row.SourceHealth,
			&row.CatalogName, &schemaName, &objectName, &objectType, &columnName, &ordinal,
			&duckDBType, &nullable, &isSystemSchema, &errorCode); err != nil {
			return nil, err
		}
		row.SchemaName = stringPointer(schemaName)
		row.ObjectName = stringPointer(objectName)
		row.ObjectType = stringPointer(objectType)
		row.ColumnName = stringPointer(columnName)
		row.DuckDBType = stringPointer(duckDBType)
		row.ErrorCode = stringPointer(errorCode)
		if ordinal.Valid {
			value := int(ordinal.Int64)
			row.OrdinalPosition = &value
		}
		if nullable.Valid {
			value := nullable.Bool
			row.Nullable = &value
		}
		if isSystemSchema.Valid {
			value := isSystemSchema.Bool
			row.IsSystemSchema = &value
		}
		metadata = append(metadata, row)
	}
	return metadata, rows.Err()
}
func (a *Adapter) Health(ctx context.Context, definition source.Definition) error {
	quotedAlias := strings.ReplaceAll(definition.Alias, "'", "''")
	var value int
	if err := a.engine.QueryRow(ctx,
		"SELECT value FROM postgres_query('"+quotedAlias+"', 'SELECT 1 AS value')").Scan(&value); err != nil {
		return fmt.Errorf("PostgreSQL health check failed: %w", err)
	}
	return nil
}
func (a *Adapter) Cleanup(ctx context.Context, definition source.Definition) error {
	return a.engine.Detach(ctx, definition.Alias, definition.ID)
}

func stringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func (a *Adapter) PostureWarnings(ctx context.Context, definition source.Definition) ([]string, error) {
	quotedAlias := strings.ReplaceAll(definition.Alias, "'", "''")
	query := "SELECT rolsuper, rolcreaterole, rolcreatedb, rolreplication, rolbypassrls " +
		"FROM postgres_query('" + quotedAlias + "', 'SELECT rolsuper, rolcreaterole, rolcreatedb, rolreplication, rolbypassrls FROM pg_roles WHERE rolname = current_user')"
	var superuser, createRole, createDB, replication, bypassRLS bool
	if err := a.engine.QueryRow(ctx, query).Scan(&superuser, &createRole, &createDB, &replication, &bypassRLS); err != nil {
		return nil, fmt.Errorf("inspect PostgreSQL role posture: %w", err)
	}
	warnings := make([]string, 0, 2)
	if superuser || createRole || createDB || replication || bypassRLS {
		warnings = append(warnings, "PostgreSQL role has elevated role attributes")
	}
	grantQuery := "SELECT write_grants FROM postgres_query('" + quotedAlias + "', " +
		"'SELECT count(*)::BIGINT AS write_grants FROM information_schema.role_table_grants WHERE grantee = current_user AND privilege_type <> ''SELECT''')"
	var writeGrants int64
	if err := a.engine.QueryRow(ctx, grantQuery).Scan(&writeGrants); err != nil {
		return nil, fmt.Errorf("inspect PostgreSQL grants: %w", err)
	}
	if writeGrants > 0 {
		warnings = append(warnings, "PostgreSQL role has non-SELECT table grants")
	}
	return warnings, nil
}

func (a *Adapter) secretValues() map[string]string {
	values := map[string]string{
		"HOST": a.config.Host, "PORT": strconv.Itoa(a.config.Port), "DATABASE": a.config.Database,
		"USER": a.config.User, "PASSWORD": a.credential.Password, "SSLMODE": a.config.SSLMode,
	}
	for key, value := range a.config.Options {
		if safeOption(key) {
			values[strings.ToUpper(key)] = value
		}
	}
	if a.credential.RootCertificatePath != "" {
		values["SSLROOTCERT"] = a.credential.RootCertificatePath
	}
	return values
}

func validSSLMode(value string) bool {
	switch value {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
		return true
	default:
		return false
	}
}

func safeOption(key string) bool {
	switch strings.ToLower(key) {
	case "connect_timeout", "application_name", "keepalives", "keepalives_idle", "options":
		return true
	default:
		return false
	}
}

var _ source.Adapter = (*Adapter)(nil)

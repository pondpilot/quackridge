package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/pondpilot/quackridge/internal/engine"
	"github.com/pondpilot/quackridge/internal/source"
)

type Config struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`
	SSLMode  string `json:"ssl_mode"`
}

type Credential struct{ Password string }

type Attacher interface {
	Attach(context.Context, engine.Attachment) error
	Detach(context.Context, string, string) error
	Query(context.Context, string, ...any) (*sql.Rows, error)
	QueryRow(context.Context, string, ...any) *sql.Row
	RegisterObjectTypes(context.Context, string, []engine.ObjectType) error
	UpdateDatabaseType(context.Context, string, string) error
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

func (*Adapter) Type() string { return "mysql" }

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
	if definition.DatabaseType != "" && definition.DatabaseType != "mysql" && definition.DatabaseType != "mariadb" {
		return fmt.Errorf("MySQL database type must be mysql or mariadb")
	}
	if a.config.Port < 1 || a.config.Port > 65535 {
		return fmt.Errorf("invalid MySQL port")
	}
	switch a.config.SSLMode {
	case "disabled", "required", "verify_ca", "verify_identity", "preferred":
	default:
		return fmt.Errorf("invalid MySQL SSL mode")
	}
	dialer := net.Dialer{Timeout: a.timeout}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(a.config.Host, strconv.Itoa(a.config.Port)))
	if err != nil {
		return fmt.Errorf("MySQL connectivity check failed: %w", err)
	}
	return connection.Close()
}

func (a *Adapter) Attach(ctx context.Context, definition source.Definition) error {
	if err := a.Validate(ctx, definition); err != nil {
		return err
	}
	databaseType := definition.DatabaseType
	if databaseType == "" {
		databaseType = "mysql"
	}
	if err := a.engine.Attach(ctx, engine.Attachment{
		SourceID: definition.ID, SourceName: definition.Name, Alias: definition.Alias,
		Type: "mysql", DatabaseType: databaseType, Secret: a.secretValues(), ReadOnly: true,
	}); err != nil {
		return err
	}
	quotedAlias := strings.ReplaceAll(definition.Alias, "'", "''")
	var version string
	if err := a.engine.QueryRow(ctx, "SELECT version FROM mysql_query('"+quotedAlias+"', 'SELECT VERSION() AS version')").Scan(&version); err != nil {
		_ = a.engine.Detach(context.Background(), definition.Alias, definition.ID)
		return fmt.Errorf("MySQL health check failed: %w", err)
	}
	if definition.DatabaseType == "" && strings.Contains(strings.ToLower(version), "mariadb") {
		if err := a.engine.UpdateDatabaseType(ctx, definition.ID, "mariadb"); err != nil {
			_ = a.engine.Detach(context.Background(), definition.Alias, definition.ID)
			return err
		}
	}
	if err := a.registerObjectTypes(ctx, definition); err != nil {
		_ = a.engine.Detach(context.Background(), definition.Alias, definition.ID)
		return err
	}
	return nil
}

func (a *Adapter) registerObjectTypes(ctx context.Context, definition source.Definition) error {
	quotedAlias := strings.ReplaceAll(definition.Alias, "'", "''")
	rows, err := a.engine.Query(ctx, "SELECT table_schema, table_name, object_type FROM mysql_query('"+quotedAlias+"', "+
		"'SELECT table_schema, table_name, CASE WHEN table_type = ''VIEW'' THEN ''view'' ELSE ''table'' END AS object_type FROM information_schema.tables')")
	if err != nil {
		return fmt.Errorf("load MySQL object metadata: %w", err)
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
	return source.ReadMetadata(ctx, a.engine, definition.ID)
}

func (a *Adapter) Health(ctx context.Context, definition source.Definition) error {
	quotedAlias := strings.ReplaceAll(definition.Alias, "'", "''")
	var value int
	if err := a.engine.QueryRow(ctx, "SELECT value FROM mysql_query('"+quotedAlias+"', 'SELECT 1 AS value')").Scan(&value); err != nil {
		return fmt.Errorf("MySQL health check failed: %w", err)
	}
	return nil
}

func (a *Adapter) Cleanup(ctx context.Context, definition source.Definition) error {
	return a.engine.Detach(ctx, definition.Alias, definition.ID)
}

func (a *Adapter) secretValues() map[string]string {
	return map[string]string{
		"HOST": a.config.Host, "PORT": strconv.Itoa(a.config.Port), "DATABASE": a.config.Database,
		"USER": a.config.User, "PASSWORD": a.credential.Password, "SSL_MODE": a.config.SSLMode,
	}
}

var _ source.Adapter = (*Adapter)(nil)

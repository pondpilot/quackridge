package postgres

import (
	"context"
	"fmt"
	"net"
	"net/url"
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
	Password string
}

type Attacher interface {
	Attach(context.Context, engine.Attachment) error
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
		if a.config.RootCertRef == "" {
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
	return a.engine.Attach(ctx, engine.Attachment{
		SourceID: definition.ID, SourceName: definition.Name, Alias: definition.Alias,
		Type: "postgres", Secret: a.secretValues(), ReadOnly: true,
	})
}

func (a *Adapter) Metadata(context.Context, source.Definition) ([]source.MetadataRow, error) {
	return nil, nil
}
func (a *Adapter) Health(context.Context, source.Definition) error  { return nil }
func (a *Adapter) Cleanup(context.Context, source.Definition) error { return nil }

func (a *Adapter) connectionString() string {
	values := url.Values{}
	values.Set("host", a.config.Host)
	values.Set("port", strconv.Itoa(a.config.Port))
	values.Set("dbname", a.config.Database)
	values.Set("user", a.config.User)
	values.Set("password", a.credential.Password)
	values.Set("sslmode", a.config.SSLMode)
	for key, value := range a.config.Options {
		if safeOption(key) {
			values.Set(key, value)
		}
	}
	parts := make([]string, 0, len(values))
	for key, entries := range values {
		for _, value := range entries {
			parts = append(parts, key+"="+quoteConnectionValue(value))
		}
	}
	return strings.Join(parts, " ")
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
	return values
}

func quoteConnectionValue(value string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), "'", `\'`) + "'"
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

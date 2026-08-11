package engine

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	quackridge "github.com/pondpilot/quackridge"
	"github.com/pondpilot/quackridge/internal/policy"
)

type Runtime struct {
	mu       sync.Mutex
	db       *sql.DB
	endpoint string
	token    string
	logger   *slog.Logger
	policy   *policy.Handle
}

// Attachment is adapter-neutral. Connection is held only for the duration of
// ATTACH and must never be logged or persisted by the engine.
type Attachment struct {
	SourceID   string
	SourceName string
	Alias      string
	Type       string
	Connection string
	ReadOnly   bool
}

func New() *Runtime { return &Runtime{} }

func (r *Runtime) Start(ctx context.Context, opts quackridge.Options) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db != nil {
		return "", &quackridge.Error{Code: quackridge.CodeInternal, Message: "engine already started"}
	}
	if opts.ExtensionDir == "" {
		return "", &quackridge.Error{Code: quackridge.CodeInternal, Message: "extension directory is required"}
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return "", internal("open DuckDB", err)
	}
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return "", internal("start DuckDB", err)
	}

	// Quack uses httpfs for its HTTP transport. Load every dependency from the
	// verified local bundle before network-based extension loading is disabled.
	for _, extension := range []string{"httpfs", "postgres_scanner", "quack"} {
		path := filepath.Join(opts.ExtensionDir, extension+".duckdb_extension")
		if err := loadExtension(ctx, db, path); err != nil {
			_ = db.Close()
			return "", err
		}
	}
	if err := configure(ctx, db, opts); err != nil {
		_ = db.Close()
		return "", err
	}
	policyHandle, err := policy.Install(ctx, db)
	if err != nil {
		_ = db.Close()
		return "", internal("install authorization policy", err)
	}
	if _, err := db.ExecContext(ctx, "SET GLOBAL quack_authorization_function = 'quackridge_authorize'"); err != nil {
		_ = policyHandle.Close()
		_ = db.Close()
		return "", internal("activate authorization policy", err)
	}

	token := opts.Token
	if token == "" {
		token, err = randomToken()
		if err != nil {
			_ = db.Close()
			return "", internal("generate token", err)
		}
	}
	host := opts.ListenHost
	if host == "" {
		host = "127.0.0.1"
	}
	if !isLoopback(host) {
		_ = db.Close()
		return "", &quackridge.Error{Code: quackridge.CodeInternal, Message: "listener must use loopback"}
	}
	port := opts.ListenPort
	if port == 0 {
		port, err = freePort(host)
		if err != nil {
			_ = db.Close()
			return "", internal("allocate loopback port", err)
		}
	}
	uri := "quack:" + net.JoinHostPort(host, strconv.Itoa(port))
	meta, _ := json.Marshal(map[string]any{
		"product": quackridge.Product, "product_version": quackridge.Version,
		"protocol_version": quackridge.ProtocolVersion, "metadata_version": quackridge.MetadataVersion,
		"source_types": []string{"postgres"}, "read_only": true, "capabilities": quackridge.Capabilities,
	})
	if _, err := db.ExecContext(ctx, "CALL quack_identify(name => ?, provider => 'local', hostname => ?, region => '', meta => ?)", "QuackRidge", host, string(meta)); err != nil {
		_ = db.Close()
		return "", internal("publish identity", err)
	}
	if err := installMetadataRelation(ctx, db); err != nil {
		_ = db.Close()
		return "", err
	}
	if _, err := db.ExecContext(ctx, "CALL quack_serve(?, token => ?)", uri, token); err != nil {
		_ = db.Close()
		return "", internal("start Quack", err)
	}
	r.db, r.endpoint, r.token, r.logger, r.policy = db, uri, token, logger, policyHandle
	logger.Info("engine ready", "component", "engine", "endpoint", uri)
	return uri, nil
}

func (r *Runtime) Reload(context.Context) error { return nil }

func (r *Runtime) Attach(ctx context.Context, attachment Attachment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil {
		return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "engine is not running"}
	}
	if !validIdentifier(attachment.Alias) || !validIdentifier(attachment.Type) {
		return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "source identifier is invalid"}
	}
	connection := strings.ReplaceAll(attachment.Connection, "'", "''")
	statement := "ATTACH '" + connection + "' AS " + quoteIdentifier(attachment.Alias) + " (TYPE " + quoteIdentifier(attachment.Type)
	if attachment.ReadOnly {
		statement += ", READ_ONLY"
	}
	statement += ")"
	if _, err := r.db.ExecContext(ctx, statement); err != nil {
		return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "source attach failed", Cause: err}
	}
	if _, err := r.db.ExecContext(ctx, `INSERT INTO memory.main.quackridge_sources VALUES (?, ?, ?, ?, 'ready', NULL)
		ON CONFLICT (source_id) DO UPDATE SET source_name = excluded.source_name,
		source_type = excluded.source_type, catalog_name = excluded.catalog_name,
		source_health = excluded.source_health, error_code = excluded.error_code`,
		attachment.SourceID, attachment.SourceName, attachment.Type, attachment.Alias); err != nil {
		_, _ = r.db.ExecContext(context.Background(), "DETACH "+quoteIdentifier(attachment.Alias))
		return internal("register source metadata", err)
	}
	r.logger.Info("source attached", "component", "source", "source_id", attachment.SourceID, "source_type", attachment.Type)
	return nil
}

func (r *Runtime) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil {
		return (&sql.DB{}).QueryRowContext(ctx, query, args...)
	}
	return r.db.QueryRowContext(ctx, query, args...)
}

func (r *Runtime) Stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil {
		return nil
	}
	_, stopErr := r.db.ExecContext(ctx, "CALL quack_stop(?)", r.endpoint)
	policyErr := r.policy.Close()
	closeErr := r.db.Close()
	r.db, r.endpoint, r.token, r.policy = nil, "", "", nil
	return errors.Join(stopErr, policyErr, closeErr)
}

func (r *Runtime) Token() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.token
}

func loadExtension(ctx context.Context, db *sql.DB, path string) error {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return &quackridge.Error{Code: quackridge.CodeInternal, Message: "required extension is unavailable", Cause: err}
	}
	quoted := strings.ReplaceAll(path, "'", "''")
	if _, err := db.ExecContext(ctx, "LOAD '"+quoted+"'"); err != nil {
		return internal("load required extension", err)
	}
	return nil
}

func configure(ctx context.Context, db *sql.DB, opts quackridge.Options) error {
	memory := opts.MemoryLimit
	if memory == "" {
		memory = "1GB"
	}
	threads := opts.Threads
	if threads <= 0 {
		threads = 4
	}
	statements := []string{
		"SET GLOBAL autoinstall_known_extensions = false",
		"SET GLOBAL autoload_known_extensions = false",
		"SET GLOBAL allow_community_extensions = false",
		fmt.Sprintf("SET GLOBAL threads = %d", threads),
		"SET GLOBAL memory_limit = '" + strings.ReplaceAll(memory, "'", "''") + "'",
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return internal("lock engine configuration", err)
		}
	}
	return nil
}

func installMetadataRelation(ctx context.Context, db *sql.DB) error {
	const sources = `CREATE TABLE quackridge_sources (
		source_id VARCHAR PRIMARY KEY, source_name VARCHAR NOT NULL, source_type VARCHAR NOT NULL,
		catalog_name VARCHAR UNIQUE NOT NULL, source_health VARCHAR NOT NULL, error_code VARCHAR)`
	if _, err := db.ExecContext(ctx, sources); err != nil {
		return internal("create source registry", err)
	}
	const statement = `CREATE OR REPLACE MACRO quackridge_metadata_v1() AS TABLE
		SELECT s.source_id, s.source_name, s.source_type, s.source_health,
		s.catalog_name, c.schema_name, c.table_name object_name,
		CASE WHEN internal THEN NULL ELSE 'table' END object_type, column_name,
		column_index + 1 ordinal_position, data_type duckdb_type, is_nullable nullable,
		s.error_code
		FROM memory.main.quackridge_sources s
		LEFT JOIN duckdb_columns() c ON c.database_name = s.catalog_name`
	if _, err := db.ExecContext(ctx, statement); err != nil {
		return internal("create metadata relation", err)
	}
	const activeQueries = `CREATE OR REPLACE MACRO quackridge_active_queries_v1() AS TABLE
		SELECT connection_id,
		regexp_extract(query, '/\*\s*quackridge-query-id:([A-Za-z0-9_-]+)\s*\*/', 1) AS query_id,
		state
		FROM quack_active_connections()
		WHERE state = 'active' AND query_id <> ''`
	if _, err := db.ExecContext(ctx, activeQueries); err != nil {
		return internal("create active-query relation", err)
	}
	return nil
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 63 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value[1:] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func quoteIdentifier(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }

func randomToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func freePort(host string) (int, error) {
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func internal(action string, err error) error {
	return &quackridge.Error{Code: quackridge.CodeInternal, Message: action + " failed", Cause: err}
}

var _ = time.Second

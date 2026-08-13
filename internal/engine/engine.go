package engine

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
	quackridge "github.com/pondpilot/quackridge"
	"github.com/pondpilot/quackridge/internal/policy"
	protocol "github.com/pondpilot/quackridge/protocol/v2"
)

type Runtime struct {
	mu               sync.Mutex
	db               *sql.DB
	endpoint         string
	token            string
	logger           *slog.Logger
	policy           *policy.Handle
	sandbox          string
	extensionDir     string
	loadedExtensions map[string]bool
	odbcConnection   *sql.Conn
	odbcResolver     *odbcResolver
	odbcHandles      map[string]int64
	sources          map[string]quackridge.SourceStatus
}

// Attachment is adapter-neutral. Connection is held only for the duration of
// ATTACH and must never be logged or persisted by the engine.
type Attachment struct {
	SourceID     string
	SourceName   string
	Alias        string
	Type         string
	DatabaseType string
	Connection   string
	Secret       map[string]string
	ReadOnly     bool
}

type ObjectType struct {
	Schema string
	Name   string
	Type   string
}

type ODBCObject struct {
	Schema       string
	Name         string
	Type         string
	RemoteSelect string
}

type ODBCAttachment struct {
	SourceID, SourceName, Alias, DatabaseType string
	Connection, Username, Password            string
	Objects                                   []ODBCObject
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
	for _, extension := range []string{"httpfs", "quack"} {
		path := filepath.Join(opts.ExtensionDir, extension+".duckdb_extension")
		if err := loadExtension(ctx, db, path); err != nil {
			_ = db.Close()
			return "", err
		}
	}
	if err := verifyVersionPair(ctx, db); err != nil {
		_ = db.Close()
		return "", err
	}
	sandbox, err := os.MkdirTemp("", "quackridge-")
	if err != nil {
		_ = db.Close()
		return "", internal("create engine sandbox", err)
	}
	if err := configure(ctx, db, opts, sandbox); err != nil {
		_ = db.Close()
		_ = os.RemoveAll(sandbox)
		return "", err
	}
	policyHandle, err := policy.Install(ctx, db, logger)
	if err != nil {
		_ = db.Close()
		_ = os.RemoveAll(sandbox)
		return "", internal("install authorization policy", err)
	}
	odbcConnection, err := db.Conn(ctx)
	if err != nil {
		_ = policyHandle.Close()
		_ = db.Close()
		_ = os.RemoveAll(sandbox)
		return "", internal("open ODBC credential resolver", err)
	}
	odbcResolver := &odbcResolver{values: make(map[string]string)}
	if err := duckdb.RegisterScalarUDF(odbcConnection, "quackridge_odbc_connection", odbcResolver); err != nil {
		_ = odbcConnection.Close()
		_ = policyHandle.Close()
		_ = db.Close()
		_ = os.RemoveAll(sandbox)
		return "", internal("install ODBC credential resolver", err)
	}
	keepODBCConnection := false
	defer func() {
		if !keepODBCConnection {
			_ = odbcConnection.Close()
		}
	}()
	if _, err := db.ExecContext(ctx, "SET GLOBAL quack_authorization_function = 'quackridge_authorize'"); err != nil {
		_ = policyHandle.Close()
		_ = db.Close()
		_ = os.RemoveAll(sandbox)
		return "", internal("activate authorization policy", err)
	}

	token := opts.Token
	if token == "" {
		token, err = randomToken()
		if err != nil {
			_ = policyHandle.Close()
			_ = db.Close()
			_ = os.RemoveAll(sandbox)
			return "", internal("generate token", err)
		}
	}
	host := opts.ListenHost
	if host == "" {
		host = "127.0.0.1"
	}
	if !isLoopback(host) {
		_ = policyHandle.Close()
		_ = db.Close()
		_ = os.RemoveAll(sandbox)
		return "", &quackridge.Error{Code: quackridge.CodeInternal, Message: "listener must use loopback"}
	}
	port := opts.ListenPort
	if port == 0 {
		port, err = freePort(host)
		if err != nil {
			_ = policyHandle.Close()
			_ = db.Close()
			_ = os.RemoveAll(sandbox)
			return "", internal("allocate loopback port", err)
		}
	}
	uri := "quack:" + net.JoinHostPort(host, strconv.Itoa(port))
	meta, _ := json.Marshal(protocol.CurrentIdentity())
	if _, err := db.ExecContext(ctx, "CALL quack_identify(name => ?, provider => 'local', hostname => ?, region => '', meta => ?)", "QuackRidge", host, string(meta)); err != nil {
		_ = policyHandle.Close()
		_ = db.Close()
		_ = os.RemoveAll(sandbox)
		return "", internal("publish identity", err)
	}
	if err := installMetadataRelation(ctx, db); err != nil {
		_ = policyHandle.Close()
		_ = db.Close()
		_ = os.RemoveAll(sandbox)
		return "", err
	}
	if err := lockConfiguration(ctx, db); err != nil {
		_ = policyHandle.Close()
		_ = db.Close()
		_ = os.RemoveAll(sandbox)
		return "", err
	}
	if _, err := db.ExecContext(ctx, "CALL quack_serve(?, token => ?)", uri, token); err != nil {
		_ = policyHandle.Close()
		_ = db.Close()
		_ = os.RemoveAll(sandbox)
		return "", internal("start Quack", err)
	}
	r.db, r.endpoint, r.token, r.logger, r.policy, r.sandbox = db, uri, token, logger, policyHandle, sandbox
	r.extensionDir = opts.ExtensionDir
	r.loadedExtensions = map[string]bool{"httpfs": true, "quack": true}
	r.odbcConnection, r.odbcResolver = odbcConnection, odbcResolver
	r.odbcHandles = make(map[string]int64)
	keepODBCConnection = true
	r.sources = make(map[string]quackridge.SourceStatus)
	logger.Info("engine ready", "component", "engine", "endpoint", uri)
	return uri, nil
}

func (r *Runtime) Reload(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil {
		return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "engine is not running"}
	}
	if err := r.db.PingContext(ctx); err != nil {
		return internal("validate engine during reload", err)
	}
	// Source replacement is introduced with the configuration reconciler. Until
	// then reload is a validated, side-effect-free transaction.
	return nil
}

func (r *Runtime) Attach(ctx context.Context, attachment Attachment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	started := time.Now()
	if r.db == nil {
		return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "engine is not running"}
	}
	if !validIdentifier(attachment.Alias) || !validIdentifier(attachment.Type) {
		return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "source identifier is invalid"}
	}
	if err := r.ensureConnectorExtension(ctx, attachment.Type); err != nil {
		r.recordSource(attachment, "unavailable", quackridge.CodeSourceUnavailable)
		return err
	}
	connection := strings.ReplaceAll(attachment.Connection, "'", "''")
	secretName := "qr_secret_" + attachment.Alias
	if len(attachment.Secret) > 0 {
		secretStatement, err := createSecretStatement(secretName, attachment.Type, attachment.Secret)
		if err != nil {
			r.recordSource(attachment, "unavailable", quackridge.CodeSourceUnavailable)
			return err
		}
		if _, err := r.db.ExecContext(ctx, secretStatement); err != nil {
			r.recordSource(attachment, "unavailable", quackridge.CodeSourceUnavailable)
			return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "source credential setup failed", Cause: err}
		}
		connection = ""
	}
	statement := "ATTACH '" + connection + "' AS " + quoteIdentifier(attachment.Alias) + " (TYPE " + quoteIdentifier(attachment.Type)
	if len(attachment.Secret) > 0 {
		statement += ", SECRET " + quoteIdentifier(secretName)
	}
	if attachment.ReadOnly {
		statement += ", READ_ONLY"
	}
	if strings.EqualFold(attachment.Type, "postgres") {
		// Do not probe or register a secret storage table in the attached
		// database. QuackRidge supplies credentials ephemerally for ATTACH.
		statement += ", SECRET_STORAGE_TABLE ''"
	}
	statement += ")"
	if _, err := r.db.ExecContext(ctx, statement); err != nil {
		if len(attachment.Secret) > 0 {
			_, _ = r.db.ExecContext(context.Background(), "DROP SECRET "+quoteIdentifier(secretName))
		}
		r.recordSource(attachment, "unavailable", quackridge.CodeSourceUnavailable)
		r.logger.Warn("source attach failed", "component", "source", "source_id", attachment.SourceID,
			"source_type", attachment.Type, "duration_ms", time.Since(started).Milliseconds(),
			"error_code", quackridge.CodeSourceUnavailable)
		return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "source attach failed", Cause: err}
	}
	databaseType := attachment.DatabaseType
	if databaseType == "" {
		databaseType = attachment.Type
	}
	if _, err := r.db.ExecContext(ctx, `INSERT INTO memory.main.quackridge_sources VALUES (?, ?, ?, ?, ?, 'ready', NULL)
		ON CONFLICT (source_id) DO UPDATE SET source_name = excluded.source_name,
		connector_type = excluded.connector_type, database_type = excluded.database_type, catalog_name = excluded.catalog_name,
		source_health = excluded.source_health, error_code = excluded.error_code`,
		attachment.SourceID, attachment.SourceName, attachment.Type, databaseType, attachment.Alias); err != nil {
		_, _ = r.db.ExecContext(context.Background(), "DETACH "+quoteIdentifier(attachment.Alias))
		return internal("register source metadata", err)
	}
	if err := r.registerAttachedObjectTypes(ctx, attachment.SourceID, attachment.Alias); err != nil {
		_, _ = r.db.ExecContext(context.Background(), "DETACH "+quoteIdentifier(attachment.Alias))
		return err
	}
	r.recordSource(attachment, "ready", "")
	r.logger.Info("source ready", "component", "source", "source_id", attachment.SourceID,
		"source_type", attachment.Type, "duration_ms", time.Since(started).Milliseconds())
	return nil
}

func (r *Runtime) ODBCQuery(ctx context.Context, sourceID, connection, username, password, query string) (*sql.Rows, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil {
		return nil, &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "engine is not running"}
	}
	if err := r.ensureConnectorExtension(ctx, "odbc"); err != nil {
		return nil, err
	}
	r.odbcResolver.set(sourceID, odbcConnectionString(connection, username, password))
	statement := "SELECT * FROM odbc_query(quackridge_odbc_connection('" + strings.ReplaceAll(sourceID, "'", "''") + "'), '" + strings.ReplaceAll(query, "'", "''") + "')"
	return r.db.QueryContext(ctx, statement)
}

func (r *Runtime) ClearODBC(sourceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if resolver := r.odbcResolver; resolver != nil {
		resolver.delete(sourceID)
	}
}

func (r *Runtime) AttachODBC(ctx context.Context, attachment ODBCAttachment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil {
		return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "engine is not running"}
	}
	if !validIdentifier(attachment.Alias) || !validIdentifier(attachment.DatabaseType) {
		return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "ODBC source identifier is invalid"}
	}
	if err := r.ensureConnectorExtension(ctx, "odbc"); err != nil {
		return err
	}
	var handle int64
	if err := r.db.QueryRowContext(ctx, "SELECT odbc_connect(?, ?, ?)", attachment.Connection, attachment.Username, attachment.Password).Scan(&handle); err != nil {
		return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "ODBC connection setup failed", Cause: err}
	}
	if _, err := r.db.ExecContext(ctx, "ATTACH ':memory:' AS "+quoteIdentifier(attachment.Alias)); err != nil {
		_ = r.closeODBCHandle(context.Background(), handle)
		return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "ODBC catalog setup failed", Cause: err}
	}
	fail := func(err error) error {
		_, _ = r.db.ExecContext(context.Background(), "DETACH "+quoteIdentifier(attachment.Alias))
		_ = r.closeODBCHandle(context.Background(), handle)
		return err
	}
	for _, object := range attachment.Objects {
		if !safeCatalogIdentifier(object.Schema) || !safeCatalogIdentifier(object.Name) || (object.Type != "table" && object.Type != "view") {
			return fail(&quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "ODBC metadata contains an unsupported identifier"})
		}
		if _, err := r.db.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+quoteIdentifier(attachment.Alias)+"."+quoteIdentifier(object.Schema)); err != nil {
			return fail(&quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "ODBC schema setup failed", Cause: err})
		}
		view := "CREATE VIEW " + quoteIdentifier(attachment.Alias) + "." + quoteIdentifier(object.Schema) + "." + quoteIdentifier(object.Name) +
			" AS SELECT * FROM odbc_query(" + strconv.FormatInt(handle, 10) + ", '" + strings.ReplaceAll(object.RemoteSelect, "'", "''") + "')"
		if _, err := r.db.ExecContext(ctx, view); err != nil {
			return fail(&quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "ODBC relation setup failed", Cause: err})
		}
	}
	if _, err := r.db.ExecContext(ctx, `INSERT INTO memory.main.quackridge_sources VALUES (?, ?, 'odbc', ?, ?, 'ready', NULL)`,
		attachment.SourceID, attachment.SourceName, attachment.DatabaseType, attachment.Alias); err != nil {
		return fail(internal("register ODBC source metadata", err))
	}
	objects := make([]ObjectType, 0, len(attachment.Objects))
	for _, object := range attachment.Objects {
		objects = append(objects, ObjectType{Schema: object.Schema, Name: object.Name, Type: object.Type})
	}
	if err := r.registerObjectTypes(ctx, attachment.SourceID, objects); err != nil {
		return fail(err)
	}
	r.odbcHandles[attachment.SourceID] = handle
	r.sources[attachment.SourceID] = quackridge.SourceStatus{ID: attachment.SourceID, Name: attachment.SourceName, Type: "odbc", Health: "ready"}
	return nil
}

func (r *Runtime) closeODBCHandle(ctx context.Context, handle int64) error {
	var ignored sql.NullString
	return r.db.QueryRowContext(ctx, "SELECT odbc_close(?)", handle).Scan(&ignored)
}

func (r *Runtime) recordSource(attachment Attachment, health string, code quackridge.ErrorCode) {
	if r.sources == nil {
		r.sources = make(map[string]quackridge.SourceStatus)
	}
	r.sources[attachment.SourceID] = quackridge.SourceStatus{
		ID: attachment.SourceID, Name: attachment.SourceName, Type: attachment.Type,
		Health: health, ErrorCode: string(code),
	}
	if r.db != nil {
		var errorCode any
		if code != "" {
			errorCode = string(code)
		}
		databaseType := attachment.DatabaseType
		if databaseType == "" {
			databaseType = attachment.Type
		}
		_, _ = r.db.ExecContext(context.Background(), `INSERT INTO memory.main.quackridge_sources VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (source_id) DO UPDATE SET source_name = excluded.source_name,
			connector_type = excluded.connector_type, database_type = excluded.database_type, catalog_name = excluded.catalog_name,
			source_health = excluded.source_health, error_code = excluded.error_code`,
			attachment.SourceID, attachment.SourceName, attachment.Type, databaseType, attachment.Alias, health, errorCode)
	}
}

func (r *Runtime) Sources() []quackridge.SourceStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	sources := make([]quackridge.SourceStatus, 0, len(r.sources))
	for _, status := range r.sources {
		sources = append(sources, status)
	}
	slices.SortFunc(sources, func(a, b quackridge.SourceStatus) int { return strings.Compare(a.ID, b.ID) })
	return sources
}

func (r *Runtime) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil {
		return (&sql.DB{}).QueryRowContext(ctx, query, args...)
	}
	return r.db.QueryRowContext(ctx, query, args...)
}

func (r *Runtime) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil {
		return nil, &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "engine is not running"}
	}
	return r.db.QueryContext(ctx, query, args...)
}

func (r *Runtime) Detach(ctx context.Context, alias, sourceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil {
		return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "engine is not running"}
	}
	if !validIdentifier(alias) {
		return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "source identifier is invalid"}
	}
	if _, err := r.db.ExecContext(ctx, "DETACH "+quoteIdentifier(alias)); err != nil {
		return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "source detach failed", Cause: err}
	}
	_, _ = r.db.ExecContext(ctx, "DROP SECRET "+quoteIdentifier("qr_secret_"+alias))
	_, _ = r.db.ExecContext(ctx, "DELETE FROM memory.main.quackridge_sources WHERE source_id = ?", sourceID)
	_, _ = r.db.ExecContext(ctx, "DELETE FROM memory.main.quackridge_objects WHERE source_id = ?", sourceID)
	delete(r.sources, sourceID)
	if handle, ok := r.odbcHandles[sourceID]; ok {
		_ = r.closeODBCHandle(context.Background(), handle)
		delete(r.odbcHandles, sourceID)
	}
	if r.odbcResolver != nil {
		r.odbcResolver.delete(sourceID)
	}
	r.logger.Info("source detached", "component", "source", "source_id", sourceID)
	return nil
}

func (r *Runtime) RegisterObjectTypes(ctx context.Context, sourceID string, objects []ObjectType) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil {
		return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "engine is not running"}
	}
	return r.registerObjectTypes(ctx, sourceID, objects)
}

func (r *Runtime) registerObjectTypes(ctx context.Context, sourceID string, objects []ObjectType) error {
	transaction, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return internal("begin metadata update", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, "DELETE FROM memory.main.quackridge_objects WHERE source_id = ?", sourceID); err != nil {
		return internal("replace object metadata", err)
	}
	for _, object := range objects {
		if object.Type != "table" && object.Type != "view" {
			return &quackridge.Error{Code: quackridge.CodeInternal, Message: "object metadata type is invalid"}
		}
		if _, err := transaction.ExecContext(ctx, "INSERT INTO memory.main.quackridge_objects VALUES (?, ?, ?, ?)",
			sourceID, object.Schema, object.Name, object.Type); err != nil {
			return internal("insert object metadata", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return internal("commit object metadata", err)
	}
	return nil
}

func (r *Runtime) UpdateDatabaseType(ctx context.Context, sourceID, databaseType string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil || !validIdentifier(databaseType) {
		return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "database type is invalid"}
	}
	if _, err := r.db.ExecContext(ctx, "UPDATE memory.main.quackridge_sources SET database_type = ? WHERE source_id = ?", databaseType, sourceID); err != nil {
		return internal("update source database type", err)
	}
	return nil
}

func (r *Runtime) Stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil {
		return nil
	}
	_, stopErr := r.db.ExecContext(ctx, "CALL quack_stop(?)", r.endpoint)
	policyErr := r.policy.Close()
	var odbcErr error
	for _, handle := range r.odbcHandles {
		odbcErr = errors.Join(odbcErr, r.closeODBCHandle(context.Background(), handle))
	}
	odbcErr = errors.Join(odbcErr, r.odbcConnection.Close())
	closeErr := r.db.Close()
	removeErr := os.RemoveAll(r.sandbox)
	r.db, r.endpoint, r.token, r.policy, r.sandbox, r.sources = nil, "", "", nil, "", nil
	r.extensionDir, r.loadedExtensions, r.odbcConnection, r.odbcResolver, r.odbcHandles = "", nil, nil, nil, nil
	return errors.Join(stopErr, policyErr, odbcErr, closeErr, removeErr)
}

func (r *Runtime) Token() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.token
}

func (r *Runtime) RotateToken(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil {
		return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "engine is not running"}
	}
	token, err := randomToken()
	if err != nil {
		return internal("rotate token", err)
	}
	previous := r.token
	if _, err := r.db.ExecContext(ctx, "CALL quack_stop(?)", r.endpoint); err != nil {
		return internal("rotate token", err)
	}
	if _, err := r.db.ExecContext(ctx, "CALL quack_serve(?, token => ?)", r.endpoint, token); err != nil {
		_, _ = r.db.ExecContext(context.Background(), "CALL quack_serve(?, token => ?)", r.endpoint, previous)
		return internal("rotate token", err)
	}
	r.token = token
	r.logger.Info("data-plane token rotated", "component", "engine")
	return nil
}

func (r *Runtime) Diagnostics(ctx context.Context) (map[string]any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil {
		return nil, &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "engine is not running"}
	}
	settings := make(map[string]string)
	rows, err := r.db.QueryContext(ctx, `SELECT name, value FROM duckdb_settings() WHERE name IN
		('memory_limit', 'max_temp_directory_size', 'threads', 'lock_configuration',
		'autoinstall_known_extensions', 'autoload_known_extensions', 'allow_community_extensions',
		'allow_unsigned_extensions', 'allow_persistent_secrets', 'disabled_filesystems')`)
	if err != nil {
		return nil, internal("read engine diagnostics", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return nil, internal("read engine diagnostics", err)
		}
		settings[name] = value
	}
	if err := rows.Err(); err != nil {
		return nil, internal("read engine diagnostics", err)
	}
	return map[string]any{
		"duckdb_version": quackridge.DuckDBVersion, "endpoint": r.endpoint,
		"source_count": len(r.sources), "settings": settings,
	}, nil
}

func loadExtension(ctx context.Context, db *sql.DB, path string) error {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return &quackridge.Error{Code: quackridge.CodeInternal, Message: "required extension is unavailable", Cause: err}
	}
	// DuckDB accepts forward slashes on every supported platform. Normalizing
	// avoids interpreting Windows path separators as SQL string escapes.
	quoted := extensionSQLPath(path)
	if _, err := db.ExecContext(ctx, "LOAD '"+quoted+"'"); err != nil {
		return internal("load required extension", err)
	}
	return nil
}

func extensionSQLPath(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	return strings.ReplaceAll(path, "'", "''")
}

func verifyVersionPair(ctx context.Context, db *sql.DB) error {
	var engineVersion string
	if err := db.QueryRowContext(ctx, "SELECT version()").Scan(&engineVersion); err != nil {
		return internal("verify DuckDB version", err)
	}
	versions := make(map[string]string)
	rows, err := db.QueryContext(ctx, `SELECT extension_name, coalesce(extension_version, '')
		FROM duckdb_extensions() WHERE loaded AND extension_name IN ('httpfs', 'quack')`)
	if err != nil {
		return internal("verify extension versions", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, version string
		if err := rows.Scan(&name, &version); err != nil {
			return internal("verify extension versions", err)
		}
		versions[name] = version
	}
	if err := rows.Err(); err != nil {
		return internal("verify extension versions", err)
	}
	return validateVersionPair(engineVersion, versions)
}

func validateVersionPair(engineVersion string, extensions map[string]string) error {
	want := quackridge.DuckDBVersion
	if strings.TrimPrefix(engineVersion, "v") != want {
		return &quackridge.Error{Code: quackridge.CodeProtocolMismatch, Message: "unsupported DuckDB version pair"}
	}
	for _, name := range []string{"httpfs", "quack"} {
		expected := quackridge.ExtensionVersions()[name]
		version, loaded := extensions[name]
		if !loaded || version != expected {
			return &quackridge.Error{Code: quackridge.CodeProtocolMismatch, Message: "unsupported DuckDB extension version pair"}
		}
	}
	return nil
}

func configure(ctx context.Context, db *sql.DB, opts quackridge.Options, sandbox string) error {
	memory := opts.MemoryLimit
	if memory == "" {
		memory = "1GB"
	}
	threads := opts.Threads
	if threads <= 0 {
		threads = 4
	}
	if threads > 64 {
		return &quackridge.Error{Code: quackridge.CodeResourceExhausted, Message: "thread limit exceeds the supported maximum"}
	}
	tempLimit := opts.TempLimit
	if tempLimit == "" {
		tempLimit = "1GB"
	}
	statements := []string{
		"SET GLOBAL autoinstall_known_extensions = false",
		"SET GLOBAL autoload_known_extensions = false",
		"SET GLOBAL allow_community_extensions = false",
		"SET GLOBAL allow_unsigned_extensions = false",
		"SET GLOBAL allow_persistent_secrets = false",
		"SET GLOBAL allow_unredacted_secrets = false",
		"SET GLOBAL default_secret_storage = 'memory'",
		"SET GLOBAL secret_directory = '" + strings.ReplaceAll(filepath.Join(sandbox, "secrets"), "'", "''") + "'",
		"SET GLOBAL temp_directory = '" + strings.ReplaceAll(filepath.Join(sandbox, "temp"), "'", "''") + "'",
		"SET GLOBAL enable_global_s3_configuration = false",
		"SET GLOBAL allowed_directories = " + quoteStringList([]string{sandbox, opts.ExtensionDir}),
		"SET GLOBAL allowed_paths = " + quoteStringList(opts.AllowedPaths),
		fmt.Sprintf("SET GLOBAL threads = %d", threads),
		"SET GLOBAL memory_limit = '" + strings.ReplaceAll(memory, "'", "''") + "'",
		"SET GLOBAL max_temp_directory_size = '" + strings.ReplaceAll(tempLimit, "'", "''") + "'",
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return internal("lock engine configuration", err)
		}
	}
	return nil
}

func lockConfiguration(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, "SET GLOBAL lock_configuration = true"); err != nil {
		return internal("lock engine configuration", err)
	}
	return nil
}

func installMetadataRelation(ctx context.Context, db *sql.DB) error {
	const sources = `CREATE TABLE quackridge_sources (
		source_id VARCHAR PRIMARY KEY, source_name VARCHAR NOT NULL, connector_type VARCHAR NOT NULL,
		database_type VARCHAR NOT NULL,
		catalog_name VARCHAR UNIQUE NOT NULL, source_health VARCHAR NOT NULL, error_code VARCHAR)`
	if _, err := db.ExecContext(ctx, sources); err != nil {
		return internal("create source registry", err)
	}
	const objects = `CREATE TABLE quackridge_objects (
		source_id VARCHAR NOT NULL, schema_name VARCHAR NOT NULL, object_name VARCHAR NOT NULL,
		object_type VARCHAR NOT NULL CHECK (object_type IN ('table', 'view')),
		PRIMARY KEY (source_id, schema_name, object_name))`
	if _, err := db.ExecContext(ctx, objects); err != nil {
		return internal("create object registry", err)
	}
	const statement = `CREATE OR REPLACE MACRO quackridge_metadata_v2() AS TABLE
		SELECT s.source_id, s.source_name, s.connector_type, s.database_type, s.source_health,
		s.catalog_name, c.schema_name, c.table_name object_name,
		CASE WHEN c.table_name IS NULL OR c.internal THEN NULL
			ELSE coalesce(o.object_type, 'table') END object_type, column_name,
		column_index + 1 ordinal_position, data_type duckdb_type, is_nullable nullable,
		CASE
			WHEN s.database_type = 'postgres' THEN lower(c.schema_name) IN ('information_schema', 'pg_catalog')
			WHEN s.database_type IN ('mysql', 'mariadb') THEN lower(c.schema_name) IN ('information_schema', 'mysql', 'performance_schema', 'sys')
			WHEN s.database_type = 'duckdb' THEN lower(c.schema_name) IN ('information_schema', 'pg_catalog')
			WHEN s.database_type = 'sqlite' THEN lower(c.schema_name) = 'temp'
			WHEN s.database_type = 'sqlserver' THEN lower(c.schema_name) IN ('information_schema', 'sys')
			WHEN s.database_type = 'oracle' THEN upper(c.schema_name) IN ('SYS', 'SYSTEM', 'OUTLN', 'XDB')
			WHEN s.database_type = 'odbc' THEN lower(c.schema_name) = 'information_schema'
			ELSE false END is_system_schema,
		s.error_code
		FROM memory.main.quackridge_sources s
		LEFT JOIN duckdb_columns() c ON c.database_name = s.catalog_name
		LEFT JOIN memory.main.quackridge_objects o ON o.source_id = s.source_id
			AND o.schema_name = c.schema_name AND o.object_name = c.table_name`
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

func (r *Runtime) ensureConnectorExtension(ctx context.Context, connectorType string) error {
	extension, supported := map[string]string{
		"postgres": "postgres_scanner",
		"mysql":    "mysql_scanner",
		"sqlite":   "sqlite_scanner",
		"odbc":     "odbc_scanner",
		"duckdb":   "",
	}[connectorType]
	if !supported {
		return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "source connector is unsupported"}
	}
	if extension == "" || r.loadedExtensions[extension] {
		return nil
	}
	if err := loadExtension(ctx, r.db, filepath.Join(r.extensionDir, extension+".duckdb_extension")); err != nil {
		return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "source connector extension is unavailable", Cause: err}
	}
	var version string
	if err := r.db.QueryRowContext(ctx, "SELECT coalesce(extension_version, '') FROM duckdb_extensions() WHERE extension_name = ? AND loaded", extension).Scan(&version); err != nil || version != quackridge.ExtensionVersions()[extension] {
		return &quackridge.Error{Code: quackridge.CodeProtocolMismatch, Message: "unsupported source connector extension version", Cause: err}
	}
	r.loadedExtensions[extension] = true
	return nil
}

func (r *Runtime) registerAttachedObjectTypes(ctx context.Context, sourceID, alias string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO memory.main.quackridge_objects
		SELECT ?, schema_name, table_name, 'table' FROM duckdb_tables() WHERE database_name = ? AND NOT internal
		UNION ALL
		SELECT ?, schema_name, view_name, 'view' FROM duckdb_views() WHERE database_name = ? AND NOT internal
		ON CONFLICT (source_id, schema_name, object_name) DO UPDATE SET object_type = excluded.object_type`,
		sourceID, alias, sourceID, alias)
	if err != nil {
		return internal("register attached object metadata", err)
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

func safeCatalogIdentifier(value string) bool {
	return value != "" && len(value) <= 255 && !strings.ContainsRune(value, 0)
}

func createSecretStatement(name, sourceType string, values map[string]string) (string, error) {
	if !validIdentifier(sourceType) {
		return "", &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "secret type is invalid"}
	}
	allowed := map[string]bool{
		"HOST": true, "HOSTADDR": true, "PORT": true, "DATABASE": true, "DBNAME": true,
		"USER": true, "PASSWORD": true, "SSLMODE": true, "SSLROOTCERT": true,
		"CONNECT_TIMEOUT": true, "APPLICATION_NAME": true, "KEEPALIVES": true,
		"KEEPALIVES_IDLE": true, "OPTIONS": true,
	}
	if sourceType == "mysql" {
		allowed = map[string]bool{
			"HOST": true, "PORT": true, "DATABASE": true, "USER": true,
			"PASSWORD": true, "SSL_MODE": true, "SSL_CA": true, "SSL_CAPATH": true,
			"SSL_CERT": true, "SSL_CIPHER": true, "SSL_CRL": true, "SSL_CRLPATH": true, "SSL_KEY": true,
		}
	}
	keys := make([]string, 0, len(values))
	normalized := make(map[string]string, len(values))
	for rawKey, value := range values {
		key := strings.ToUpper(rawKey)
		if !allowed[key] {
			return "", &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "secret option is invalid"}
		}
		if _, duplicate := normalized[key]; duplicate {
			return "", &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "secret option is duplicated"}
		}
		normalized[key] = value
		keys = append(keys, key)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys)+1)
	parts = append(parts, "TYPE "+sourceType)
	for _, key := range keys {
		parts = append(parts, key+" '"+strings.ReplaceAll(normalized[key], "'", "''")+"'")
	}
	return "CREATE OR REPLACE SECRET " + quoteIdentifier(name) + " (" + strings.Join(parts, ", ") + ")", nil
}

func quoteIdentifier(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }

func quoteStringList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			quoted = append(quoted, "'"+strings.ReplaceAll(value, "'", "''")+"'")
		}
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

type odbcResolver struct {
	mu     sync.RWMutex
	values map[string]string
}

func (r *odbcResolver) set(sourceID, value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[sourceID] = value
}

func (r *odbcResolver) delete(sourceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.values, sourceID)
}

func (*odbcResolver) Config() duckdb.ScalarFuncConfig {
	varchar, _ := duckdb.NewTypeInfo(duckdb.TYPE_VARCHAR)
	return duckdb.ScalarFuncConfig{InputTypeInfos: []duckdb.TypeInfo{varchar}, ResultTypeInfo: varchar}
}

func (r *odbcResolver) Executor() duckdb.ScalarFuncExecutor {
	return duckdb.ScalarFuncExecutor{RowExecutor: func(values []driver.Value) (any, error) {
		sourceID, _ := values[0].(string)
		r.mu.RLock()
		defer r.mu.RUnlock()
		value, ok := r.values[sourceID]
		if !ok {
			return nil, errors.New("ODBC source is unavailable")
		}
		return value, nil
	}}
}

func odbcConnectionString(connection, username, password string) string {
	value := strings.TrimSuffix(connection, ";")
	if username != "" {
		value += ";UID={" + strings.ReplaceAll(username, "}", "}}") + "}"
	}
	if password != "" {
		value += ";PWD={" + strings.ReplaceAll(password, "}", "}}") + "}"
	}
	return value
}

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

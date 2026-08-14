package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	quackridge "github.com/pondpilot/quackridge"
	"github.com/pondpilot/quackridge/internal/certstore"
	"github.com/pondpilot/quackridge/internal/config"
	"github.com/pondpilot/quackridge/internal/engine"
	"github.com/pondpilot/quackridge/internal/reconcile"
	"github.com/pondpilot/quackridge/internal/secrets"
	"github.com/pondpilot/quackridge/internal/source"
)

type Runtime struct {
	mu               sync.RWMutex
	engine           *engine.Runtime
	manager          *reconcile.Manager
	failures         []quackridge.SourceStatus
	logger           *slog.Logger
	loader           reconcile.Loader
	secrets          secrets.Store
	options          quackridge.Options
	basePaths        []string
	document         config.Document
	health           map[string]healthSnapshot
	healthCancel     context.CancelFunc
	healthGeneration uint64
	backoff          *source.Backoff
	certificates     *certstore.Store
}

type healthSnapshot struct {
	health  string
	checked time.Time
	retry   *time.Time
}

func New(loader reconcile.Loader, credentialStore secrets.Store) (*Runtime, error) {
	engineRuntime := engine.New()
	var certificates *certstore.Store
	if configured, ok := loader.(StoreLoader); ok {
		store := certstore.Store{Root: filepath.Join(filepath.Dir(configured.Store.Path), "state-v2", "certificates")}
		certificates = &store
	}
	manager, err := newManager(loader, credentialStore, engineRuntime, certificates)
	if err != nil {
		return nil, err
	}
	return &Runtime{engine: engineRuntime, manager: manager, loader: loader, secrets: credentialStore,
		health: make(map[string]healthSnapshot), backoff: source.NewBackoff(5*time.Second, 5*time.Minute), certificates: certificates}, nil
}

func newManager(loader reconcile.Loader, credentialStore secrets.Store, engineRuntime *engine.Runtime, certificates reconcile.RootCertificateResolver) (*reconcile.Manager, error) {
	return reconcile.New(loader, credentialStore,
		reconcile.PostgresFactory{Attacher: engineRuntime, Certificates: certificates},
		reconcile.MySQLFactory{Attacher: engineRuntime},
		reconcile.FileFactory{Connector: "sqlite", Attacher: engineRuntime},
		reconcile.FileFactory{Connector: "duckdb", Attacher: engineRuntime},
		reconcile.ODBCFactory{Attacher: engineRuntime},
	)
}

func (r *Runtime) Start(ctx context.Context, options quackridge.Options) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	document, err := r.loader.Load()
	if err != nil {
		return "", &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "load source configuration failed", Cause: err}
	}
	r.basePaths = mergePaths(options.AllowedPaths)
	options.AllowedPaths = mergePaths(r.basePaths, filePaths(document))
	endpoint, err := r.engine.Start(ctx, options)
	if err != nil {
		return "", err
	}
	r.logger = options.Logger
	if host, port, parseErr := endpointAddress(endpoint); parseErr == nil {
		options.ListenHost, options.ListenPort = host, port
	}
	options.Token = r.engine.Token()
	r.options = options
	r.document = document.Clone()
	failures, bootstrapErr := r.manager.Bootstrap(ctx)
	if bootstrapErr != nil {
		_ = r.engine.Stop(context.Background())
		return "", &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "load source configuration failed", Cause: bootstrapErr}
	}
	r.failures = make([]quackridge.SourceStatus, 0, len(failures))
	for _, failure := range failures {
		r.failures = append(r.failures, quackridge.SourceStatus{
			ID: failure.ID, Name: failure.Name, Type: failure.Type,
			Health: "unavailable", Enabled: true, ErrorCode: string(quackridge.CodeSourceUnavailable),
		})
		if r.logger != nil {
			r.logger.Warn("source unavailable", "component", "source", "source_id", failure.ID,
				"source_type", failure.Type, "error_code", quackridge.CodeSourceUnavailable)
		}
	}
	r.startHealthSchedulerLocked()
	return endpoint, nil
}

func (r *Runtime) Reload(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.healthGeneration++
	document, err := r.loader.Load()
	if err != nil {
		return err
	}
	nextPaths := mergePaths(r.basePaths, filePaths(document))
	if !slices.Equal(nextPaths, r.options.AllowedPaths) {
		return r.rebuild(ctx, document, nextPaths)
	}
	if err := r.manager.Reload(ctx); err != nil {
		return err
	}
	if err := r.engine.Reload(ctx); err != nil {
		return err
	}
	r.failures = nil
	r.document = document.Clone()
	r.pruneHealthLocked(document)
	return nil
}

// rebuild validates the complete candidate against a disposable engine before
// replacing the locked engine. The replacement keeps the same endpoint and
// token, so already-paired browser clients reconnect without changing config.
func (r *Runtime) rebuild(ctx context.Context, document config.Document, allowedPaths []string) error {
	previousDocument := r.document.Clone()
	previousOptions := r.options
	options := r.options
	options.AllowedPaths = allowedPaths

	probeEngine := engine.New()
	probeManager, err := newManager(r.loader, r.secrets, probeEngine, r.certificates)
	if err != nil {
		return err
	}
	probeOptions := options
	probeOptions.ListenPort = 0
	probeOptions.Token = ""
	if _, err := probeEngine.Start(ctx, probeOptions); err != nil {
		return fmt.Errorf("validate reloaded engine: %w", err)
	}
	if err := probeManager.Reload(ctx); err != nil {
		_ = probeEngine.Stop(context.Background())
		return err
	}
	if err := probeEngine.Stop(ctx); err != nil {
		return fmt.Errorf("stop reload validation engine: %w", err)
	}

	if err := r.engine.Stop(ctx); err != nil {
		restoreErr := r.restore(previousDocument, previousOptions)
		return errors.Join(err, restoreErr)
	}
	replacement := engine.New()
	replacementManager, err := newManager(r.loader, r.secrets, replacement, r.certificates)
	if err != nil {
		return err
	}
	if _, err := replacement.Start(ctx, options); err != nil {
		restoreErr := r.restore(previousDocument, previousOptions)
		return errors.Join(fmt.Errorf("restart engine for file-source allowlist: %w", err), restoreErr)
	}
	if err := replacementManager.Reload(ctx); err != nil {
		_ = replacement.Stop(context.Background())
		restoreErr := r.restore(previousDocument, previousOptions)
		return errors.Join(fmt.Errorf("reattach sources after engine restart: %w", err), restoreErr)
	}
	r.engine, r.manager, r.options = replacement, replacementManager, options
	r.failures = nil
	r.document = document.Clone()
	return nil
}

func (r *Runtime) restore(document config.Document, options quackridge.Options) error {
	restoreCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	restored := engine.New()
	loader := documentLoader{document: document}
	manager, err := newManager(loader, r.secrets, restored, r.certificates)
	if err != nil {
		return fmt.Errorf("restore previous engine: %w", err)
	}
	if _, err := restored.Start(restoreCtx, options); err != nil {
		return fmt.Errorf("restore previous engine: %w", err)
	}
	if err := manager.Reload(restoreCtx); err != nil {
		_ = restored.Stop(context.Background())
		return fmt.Errorf("restore previous sources: %w", err)
	}
	if err := manager.RebindLoader(r.loader); err != nil {
		_ = restored.Stop(context.Background())
		return fmt.Errorf("restore live configuration loader: %w", err)
	}
	r.engine, r.manager, r.options = restored, manager, options
	r.document = document.Clone()
	return nil
}

func filePaths(document config.Document) []string {
	var paths []string
	for _, configured := range document.Sources {
		if !configured.Enabled || (configured.Type != "sqlite" && configured.Type != "duckdb") {
			continue
		}
		var options struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(configured.Options, &options) == nil && options.Path != "" {
			paths = append(paths, options.Path)
			if configured.Type == "duckdb" {
				paths = append(paths, options.Path+".wal")
			}
		}
	}
	return paths
}

func mergePaths(groups ...[]string) []string {
	set := make(map[string]struct{})
	for _, group := range groups {
		for _, path := range group {
			if path != "" {
				set[path] = struct{}{}
			}
		}
	}
	paths := make([]string, 0, len(set))
	for path := range set {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths
}

func endpointAddress(endpoint string) (string, int, error) {
	host, rawPort, err := net.SplitHostPort(strings.TrimPrefix(endpoint, "quack:"))
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(rawPort)
	return host, port, err
}

func (r *Runtime) Stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.healthCancel != nil {
		r.healthCancel()
		r.healthCancel = nil
	}
	r.healthGeneration++
	err := r.engine.Stop(ctx)
	r.failures = nil
	return err
}

func (r *Runtime) Sources() []quackridge.SourceStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sourcesLocked()
}

func (r *Runtime) sourcesLocked() []quackridge.SourceStatus {
	sources := r.engine.Sources()
	byID := make(map[string]quackridge.SourceStatus, len(sources)+len(r.failures))
	for _, status := range sources {
		byID[status.ID] = status
	}
	for _, status := range r.failures {
		byID[status.ID] = status
	}
	for id, current := range byID {
		current.Enabled = sourceEnabled(r.document, id)
		if health, ok := r.health[id]; ok {
			current.Health = health.health
			checked := health.checked
			current.LastCheckAt = &checked
			current.NextRetryAt = health.retry
			if health.health == "ready" {
				current.ErrorCode = ""
			} else {
				current.ErrorCode = string(quackridge.CodeSourceUnavailable)
			}
		}
		byID[id] = current
	}
	sources = sources[:0]
	for _, status := range byID {
		sources = append(sources, status)
	}
	slices.SortFunc(sources, func(a, b quackridge.SourceStatus) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return sources
}

func (r *Runtime) startHealthSchedulerLocked() {
	if r.healthCancel != nil {
		r.healthCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.healthCancel = cancel
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = r.probeHealth(ctx, "", false)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (r *Runtime) probeHealth(parent context.Context, requested string, force bool) error {
	r.mu.RLock()
	generation := r.healthGeneration
	manager := r.manager
	now := time.Now().UTC()
	ids := make([]string, 0, len(r.document.Sources))
	found := requested == ""
	for _, configured := range r.document.Sources {
		if configured.ID == requested {
			found = true
		}
		if !configured.Enabled || requested != "" && configured.ID != requested {
			continue
		}
		if health, ok := r.health[configured.ID]; !force && ok && health.retry != nil && now.Before(*health.retry) {
			continue
		}
		ids = append(ids, configured.ID)
	}
	r.mu.RUnlock()
	if !found {
		return &quackridge.Error{Code: quackridge.CodeValidation, Message: "source was not found"}
	}
	if len(ids) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	results := manager.DiagnosticsFor(ctx, ids...)
	now = time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	if generation != r.healthGeneration {
		return nil
	}
	for _, result := range results {
		if result.Health == "ready" {
			r.backoff.Ready(result.ID)
			r.health[result.ID] = healthSnapshot{health: "ready", checked: now}
		} else {
			next := now.Add(r.backoff.Failure(result.ID))
			r.health[result.ID] = healthSnapshot{health: "unavailable", checked: now, retry: &next}
		}
	}
	return nil
}

func (r *Runtime) RefreshSourceHealth(ctx context.Context, id string) error {
	return r.probeHealth(ctx, id, true)
}

func (r *Runtime) pruneHealthLocked(document config.Document) {
	for id := range r.health {
		if !hasSource(document, id) {
			delete(r.health, id)
			r.backoff.Ready(id)
		}
	}
}

func hasSource(document config.Document, id string) bool {
	for _, configured := range document.Sources {
		if configured.ID == id {
			return true
		}
	}
	return false
}
func sourceEnabled(document config.Document, id string) bool {
	for _, configured := range document.Sources {
		if configured.ID == id {
			return configured.Enabled
		}
	}
	return false
}

func (r *Runtime) Token() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.engine.Token()
}
func (r *Runtime) RotateToken(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.engine.RotateToken(ctx); err != nil {
		return err
	}
	r.options.Token = r.engine.Token()
	return nil
}
func (r *Runtime) Diagnostics(ctx context.Context) (map[string]any, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	diagnosticCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	diagnostics, err := r.engine.Diagnostics(diagnosticCtx)
	if err != nil {
		return nil, err
	}
	diagnostics["sources"] = r.sourcesLocked()
	diagnostics["source_diagnostics"] = r.manager.Diagnostics(diagnosticCtx)
	diagnostics["product_version"] = quackridge.Version
	diagnostics["protocol_version"] = quackridge.ProtocolVersion
	diagnostics["capabilities"] = quackridge.Capabilities()
	diagnostics["extension_versions"] = quackridge.ExtensionVersions()
	return diagnostics, nil
}

type StoreLoader struct{ Store config.Store }

func (l StoreLoader) Load() (config.Document, error) { return l.Store.Load() }

type documentLoader struct{ document config.Document }

func (l documentLoader) Load() (config.Document, error) { return l.document.Clone(), nil }

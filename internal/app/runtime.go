package app

import (
	"context"
	"log/slog"
	"slices"
	"sync"

	quackridge "github.com/pondpilot/quackridge"
	"github.com/pondpilot/quackridge/internal/config"
	"github.com/pondpilot/quackridge/internal/engine"
	"github.com/pondpilot/quackridge/internal/reconcile"
	"github.com/pondpilot/quackridge/internal/secrets"
)

type Runtime struct {
	mu       sync.RWMutex
	engine   *engine.Runtime
	manager  *reconcile.Manager
	failures []quackridge.SourceStatus
	logger   *slog.Logger
}

func New(loader reconcile.Loader, credentialStore secrets.Store) (*Runtime, error) {
	engineRuntime := engine.New()
	manager, err := reconcile.New(loader, credentialStore, reconcile.PostgresFactory{Attacher: engineRuntime})
	if err != nil {
		return nil, err
	}
	return &Runtime{engine: engineRuntime, manager: manager}, nil
}

func (r *Runtime) Start(ctx context.Context, options quackridge.Options) (string, error) {
	endpoint, err := r.engine.Start(ctx, options)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	r.logger = options.Logger
	r.mu.Unlock()
	failures, bootstrapErr := r.manager.Bootstrap(ctx)
	if bootstrapErr != nil {
		_ = r.engine.Stop(context.Background())
		return "", &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "load source configuration failed", Cause: bootstrapErr}
	}
	r.mu.Lock()
	r.failures = make([]quackridge.SourceStatus, 0, len(failures))
	for _, failure := range failures {
		r.failures = append(r.failures, quackridge.SourceStatus{
			ID: failure.ID, Name: failure.Name, Type: failure.Type,
			Health: "unavailable", ErrorCode: string(quackridge.CodeSourceUnavailable),
		})
		if r.logger != nil {
			r.logger.Warn("source unavailable", "component", "source", "source_id", failure.ID,
				"source_type", failure.Type, "error_code", quackridge.CodeSourceUnavailable)
		}
	}
	r.mu.Unlock()
	return endpoint, nil
}

func (r *Runtime) Reload(ctx context.Context) error {
	if err := r.manager.Reload(ctx); err != nil {
		return err
	}
	if err := r.engine.Reload(ctx); err != nil {
		return err
	}
	r.mu.Lock()
	r.failures = nil
	r.mu.Unlock()
	return nil
}

func (r *Runtime) Stop(ctx context.Context) error {
	err := r.engine.Stop(ctx)
	r.mu.Lock()
	r.failures = nil
	r.mu.Unlock()
	return err
}

func (r *Runtime) Sources() []quackridge.SourceStatus {
	sources := r.engine.Sources()
	r.mu.RLock()
	byID := make(map[string]quackridge.SourceStatus, len(sources)+len(r.failures))
	for _, status := range sources {
		byID[status.ID] = status
	}
	for _, status := range r.failures {
		byID[status.ID] = status
	}
	r.mu.RUnlock()
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

func (r *Runtime) Token() string                         { return r.engine.Token() }
func (r *Runtime) RotateToken(ctx context.Context) error { return r.engine.RotateToken(ctx) }
func (r *Runtime) Diagnostics(ctx context.Context) (map[string]any, error) {
	return r.engine.Diagnostics(ctx)
}

type StoreLoader struct{ Store config.Store }

func (l StoreLoader) Load() (config.Document, error) { return l.Store.Load() }

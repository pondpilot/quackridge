package reconcile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sync"

	quackridge "github.com/pondpilot/quackridge"
	"github.com/pondpilot/quackridge/internal/config"
	"github.com/pondpilot/quackridge/internal/secrets"
	"github.com/pondpilot/quackridge/internal/source"
)

type Loader interface {
	Load() (config.Document, error)
}

type Factory interface {
	Type() string
	Build(config.Source, []byte) (source.Adapter, error)
}

type activeSource struct {
	definition  source.Definition
	adapter     source.Adapter
	fingerprint [32]byte
}

type preparedSource struct {
	definition  source.Definition
	adapter     source.Adapter
	fingerprint [32]byte
}

// Manager validates an entire candidate configuration and every credential
// before mutating healthy attachments. Apply operations carry rollback actions
// so a failed reload preserves the previous active set.
type Manager struct {
	mu        sync.Mutex
	loader    Loader
	secrets   secrets.Store
	factories map[string]Factory
	active    map[string]activeSource
}

func New(loader Loader, credentialStore secrets.Store, factories ...Factory) (*Manager, error) {
	if loader == nil || credentialStore == nil {
		return nil, errors.New("configuration and credential stores are required")
	}
	manager := &Manager{
		loader: loader, secrets: credentialStore,
		factories: make(map[string]Factory, len(factories)), active: make(map[string]activeSource),
	}
	for _, factory := range factories {
		if factory == nil || factory.Type() == "" {
			return nil, errors.New("source factory type is required")
		}
		if _, exists := manager.factories[factory.Type()]; exists {
			return nil, fmt.Errorf("duplicate source factory %q", factory.Type())
		}
		manager.factories[factory.Type()] = factory
	}
	return manager, nil
}

func (m *Manager) Reload(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	prepared, err := m.prepare(ctx)
	if err != nil {
		return err
	}
	return m.apply(ctx, prepared)
}

func (m *Manager) Validate(ctx context.Context, configured config.Source, credential []byte) error {
	factory, ok := m.factories[configured.Type]
	if !ok {
		return errors.New("source adapter is unavailable")
	}
	adapter, err := factory.Build(configured, credential)
	if err != nil {
		return err
	}
	return adapter.Validate(ctx, source.Definition{
		ID: configured.ID, Name: configured.Name, Alias: configured.Alias,
		Type: configured.Type, Enabled: configured.Enabled,
	})
}

func (m *Manager) prepare(ctx context.Context) (map[string]preparedSource, error) {
	document, err := m.loader.Load()
	if err != nil {
		return nil, &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "source configuration is invalid", Cause: err}
	}
	prepared := make(map[string]preparedSource)
	for _, configured := range document.Sources {
		if !configured.Enabled {
			continue
		}
		factory, ok := m.factories[configured.Type]
		if !ok {
			return nil, &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "source adapter is unavailable"}
		}
		credential, err := m.secrets.Get(ctx, configured.CredentialRef)
		if err != nil {
			return nil, &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "source credential is unavailable", Cause: err}
		}
		configuredFingerprint := fingerprint(configured, credential)
		adapter, buildErr := factory.Build(configured, credential)
		clear(credential)
		if buildErr != nil {
			return nil, &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "source configuration is invalid", Cause: buildErr}
		}
		definition := source.Definition{
			ID: configured.ID, Name: configured.Name, Alias: configured.Alias,
			Type: configured.Type, Enabled: configured.Enabled,
		}
		if err := adapter.Validate(ctx, definition); err != nil {
			return nil, &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "source validation failed", Cause: err}
		}
		prepared[configured.ID] = preparedSource{
			definition: definition, adapter: adapter, fingerprint: configuredFingerprint,
		}
	}
	return prepared, nil
}

func (m *Manager) apply(ctx context.Context, prepared map[string]preparedSource) (err error) {
	var undo []func()
	defer func() {
		if err == nil {
			return
		}
		for index := len(undo) - 1; index >= 0; index-- {
			undo[index]()
		}
	}()

	ids := make([]string, 0, len(prepared))
	for id := range prepared {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	next := make(map[string]activeSource, len(prepared))
	for _, id := range ids {
		candidate := prepared[id]
		current, exists := m.active[id]
		if exists && current.fingerprint == candidate.fingerprint {
			next[id] = current
			continue
		}
		if !exists {
			if err := candidate.adapter.Attach(ctx, candidate.definition); err != nil {
				return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "source attach failed", Cause: err}
			}
			attached := candidate
			undo = append(undo, func() { _ = attached.adapter.Cleanup(context.Background(), attached.definition) })
			next[id] = activeSource(candidate)
			continue
		}

		staged := candidate.definition
		staged.ID = "stage_" + id
		staged.Alias = stageAlias(candidate.definition.Alias, candidate.fingerprint)
		if err := candidate.adapter.Attach(ctx, staged); err != nil {
			return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "replacement source validation failed", Cause: err}
		}
		if err := candidate.adapter.Cleanup(ctx, staged); err != nil {
			return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "replacement source cleanup failed", Cause: err}
		}
		if err := current.adapter.Cleanup(ctx, current.definition); err != nil {
			return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "existing source cleanup failed", Cause: err}
		}
		if err := candidate.adapter.Attach(ctx, candidate.definition); err != nil {
			_ = current.adapter.Attach(context.Background(), current.definition)
			return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "replacement source attach failed", Cause: err}
		}
		old, replacement := current, candidate
		undo = append(undo, func() {
			_ = replacement.adapter.Cleanup(context.Background(), replacement.definition)
			_ = old.adapter.Attach(context.Background(), old.definition)
		})
		next[id] = activeSource(candidate)
	}

	for id, current := range m.active {
		if _, remains := prepared[id]; remains {
			continue
		}
		if err := current.adapter.Cleanup(ctx, current.definition); err != nil {
			return &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "removed source cleanup failed", Cause: err}
		}
		removed := current
		undo = append(undo, func() { _ = removed.adapter.Attach(context.Background(), removed.definition) })
	}
	m.active = next
	return nil
}

func fingerprint(configured config.Source, credential []byte) [32]byte {
	credentialHash := sha256.Sum256(credential)
	value := bytes.Join([][]byte{
		[]byte(configured.ID), []byte(configured.Name), []byte(configured.Alias),
		[]byte(configured.Type), []byte(configured.CredentialRef), configured.Options, credentialHash[:],
	}, []byte{0})
	return sha256.Sum256(value)
}

func stageAlias(alias string, fingerprint [32]byte) string {
	suffix := hex.EncodeToString(fingerprint[:4])
	maximumPrefix := 63 - len("qr_stage__") - len(suffix)
	if len(alias) > maximumPrefix {
		alias = alias[:maximumPrefix]
	}
	return "qr_stage_" + alias + "_" + suffix
}

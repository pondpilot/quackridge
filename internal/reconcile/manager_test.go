package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/pondpilot/quackridge/internal/config"
	"github.com/pondpilot/quackridge/internal/secrets"
	"github.com/pondpilot/quackridge/internal/source"
)

type testLoader struct{ document config.Document }

func (l *testLoader) Load() (config.Document, error) { return l.document.Clone(), nil }

type adapterBehavior struct {
	FailValidation bool `json:"fail_validation"`
	FailAttach     bool `json:"fail_attach"`
}

type adapterEvents struct {
	mu       sync.Mutex
	attached []string
	cleaned  []string
}

type testFactory struct{ events *adapterEvents }

func (testFactory) Type() string { return "test" }
func (f testFactory) Build(configured config.Source, _ []byte) (source.Adapter, error) {
	var behavior adapterBehavior
	if err := json.Unmarshal(configured.Options, &behavior); err != nil {
		return nil, err
	}
	return &testAdapter{events: f.events, behavior: behavior}, nil
}

type testAdapter struct {
	events   *adapterEvents
	behavior adapterBehavior
}

func (*testAdapter) Type() string { return "test" }
func (a *testAdapter) Validate(context.Context, source.Definition) error {
	if a.behavior.FailValidation {
		return errors.New("validation failed")
	}
	return nil
}
func (a *testAdapter) Attach(_ context.Context, definition source.Definition) error {
	if a.behavior.FailAttach {
		return errors.New("attach failed")
	}
	a.events.mu.Lock()
	defer a.events.mu.Unlock()
	a.events.attached = append(a.events.attached, definition.ID)
	return nil
}
func (*testAdapter) Metadata(context.Context, source.Definition) ([]source.MetadataRow, error) {
	return nil, nil
}
func (*testAdapter) Health(context.Context, source.Definition) error { return nil }
func (a *testAdapter) Cleanup(_ context.Context, definition source.Definition) error {
	a.events.mu.Lock()
	defer a.events.mu.Unlock()
	a.events.cleaned = append(a.events.cleaned, definition.ID)
	return nil
}

func configured(id string, behavior adapterBehavior) config.Source {
	options, _ := json.Marshal(behavior)
	return config.Source{
		ID: id, Name: id, Alias: id, Type: "test", Enabled: true,
		CredentialRef: "source/" + id, Options: options,
	}
}

func TestReloadValidatesEverythingBeforeMutation(t *testing.T) {
	loader := &testLoader{document: config.Document{Version: config.CurrentVersion, Sources: []config.Source{configured("warehouse", adapterBehavior{})}}}
	credentials := secrets.NewMemory()
	for _, id := range []string{"warehouse", "broken"} {
		if err := credentials.Put(t.Context(), "source/"+id, []byte("password")); err != nil {
			t.Fatal(err)
		}
	}
	events := &adapterEvents{}
	manager, err := New(loader, credentials, testFactory{events: events})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	loader.document.Sources = append(loader.document.Sources, configured("broken", adapterBehavior{FailValidation: true}))
	if err := manager.Reload(t.Context()); err == nil {
		t.Fatal("invalid reload succeeded")
	}
	events.mu.Lock()
	defer events.mu.Unlock()
	if len(events.attached) != 1 || events.attached[0] != "warehouse" || len(events.cleaned) != 0 {
		t.Fatalf("validation mutated active sources: attached=%v cleaned=%v", events.attached, events.cleaned)
	}
}

func TestReloadRollsBackNewAttachments(t *testing.T) {
	loader := &testLoader{document: config.Document{Version: config.CurrentVersion, Sources: []config.Source{configured("warehouse", adapterBehavior{})}}}
	credentials := secrets.NewMemory()
	for _, id := range []string{"warehouse", "alpha", "broken"} {
		_ = credentials.Put(t.Context(), "source/"+id, []byte("password"))
	}
	events := &adapterEvents{}
	manager, _ := New(loader, credentials, testFactory{events: events})
	if err := manager.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	loader.document.Sources = append(loader.document.Sources,
		configured("alpha", adapterBehavior{}), configured("broken", adapterBehavior{FailAttach: true}))
	if err := manager.Reload(t.Context()); err == nil {
		t.Fatal("failing attach reload succeeded")
	}
	events.mu.Lock()
	defer events.mu.Unlock()
	if len(events.cleaned) != 1 || events.cleaned[0] != "alpha" {
		t.Fatalf("rollback cleanup = %v; attached=%v", events.cleaned, events.attached)
	}
}

func TestBootstrapKeepsHealthySourcesWhenOneFails(t *testing.T) {
	loader := &testLoader{document: config.Document{Version: config.CurrentVersion, Sources: []config.Source{
		configured("warehouse", adapterBehavior{}),
		configured("broken", adapterBehavior{FailValidation: true}),
	}}}
	credentials := secrets.NewMemory()
	for _, id := range []string{"warehouse", "broken"} {
		_ = credentials.Put(t.Context(), "source/"+id, []byte("password"))
	}
	events := &adapterEvents{}
	manager, err := New(loader, credentials, testFactory{events: events})
	if err != nil {
		t.Fatal(err)
	}
	failures, err := manager.Bootstrap(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0].ID != "broken" {
		t.Fatalf("failures = %#v", failures)
	}
	events.mu.Lock()
	defer events.mu.Unlock()
	if len(events.attached) != 1 || events.attached[0] != "warehouse" {
		t.Fatalf("attached = %v", events.attached)
	}
}

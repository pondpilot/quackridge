package config

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	quackridge "github.com/pondpilot/quackridge"
	"github.com/pondpilot/quackridge/internal/secrets"
)

type acceptingValidator struct{}

func (acceptingValidator) Validate(context.Context, Source, []byte) error { return nil }

type reconcilerFunc func(context.Context) error

func (f reconcilerFunc) Reload(ctx context.Context) error { return f(ctx) }

type faultStore struct {
	base       *secrets.Memory
	deleteErr  error
	deletedRef string
}

func (s *faultStore) Get(ctx context.Context, ref string) ([]byte, error) {
	return s.base.Get(ctx, ref)
}
func (s *faultStore) Put(ctx context.Context, ref string, value []byte) error {
	return s.base.Put(ctx, ref, value)
}
func (s *faultStore) Delete(ctx context.Context, ref string) error {
	s.deletedRef = ref
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.base.Delete(ctx, ref)
}

func transactionSource(id string) Source {
	return Source{ID: id, Name: "Warehouse", Alias: id, Type: "postgres", DatabaseType: "postgres", Enabled: true,
		Options: json.RawMessage(`{"host":"127.0.0.1","port":5432,"database":"analytics","user":"reader","ssl_mode":"require"}`)}
}

func TestTransactionalServiceRejectsStaleRevisionWithoutSideEffects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	credentials := secrets.NewMemory()
	service := TransactionalService{Store: Store{Path: path}, Credentials: credentials, Validator: acceptingValidator{}}
	_, revision, err := service.Apply(t.Context(), Mutation{Operation: "add", Source: transactionSource("warehouse"), ExpectedRevision: "stale", CredentialAction: CredentialReplace, Credential: []byte("marker-secret")})
	var public *quackridge.Error
	if !errors.As(err, &public) || public.Code != quackridge.CodeConflict || revision == "" {
		t.Fatalf("Apply() = revision %q, error %v", revision, err)
	}
	document, loadErr := (Store{Path: path}).Load()
	if loadErr != nil || len(document.Sources) != 0 {
		t.Fatalf("document = %#v, %v", document, loadErr)
	}
}

func TestTransactionalServiceRejectsCredentialOnNonCredentialMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	service := TransactionalService{Store: Store{Path: path}, Credentials: secrets.NewMemory(), Validator: acceptingValidator{}}
	_, _, err := service.Apply(t.Context(), Mutation{Operation: "remove", SourceID: "warehouse", CredentialAction: CredentialReplace, Credential: []byte("must-not-store")})
	var public *quackridge.Error
	if !errors.As(err, &public) || public.Code != quackridge.CodeValidation {
		t.Fatalf("error = %v", err)
	}
}

func TestTransactionalServiceUsesVersionedReferenceAndNeverPersistsSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	credentials := secrets.NewMemory()
	service := TransactionalService{Store: Store{Path: path}, Credentials: credentials, Validator: acceptingValidator{}}
	document, revision, err := service.Apply(t.Context(), Mutation{Operation: "add", Source: transactionSource("warehouse"), CredentialAction: CredentialReplace, Credential: []byte("marker-secret")})
	if err != nil {
		t.Fatal(err)
	}
	ref := document.Sources[0].CredentialRef
	if !strings.HasPrefix(ref, "quackridge/source/warehouse/") || len(revision) != 64 {
		t.Fatalf("ref = %q, revision = %q", ref, revision)
	}
	value, err := credentials.Get(t.Context(), ref)
	if err != nil || string(value) != "marker-secret" {
		t.Fatalf("credential = %q, %v", value, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(data), "marker-secret") {
		t.Fatalf("persisted secret: %v, %q", err, data)
	}
}

func TestTransactionalServiceRuntimeFailureRollsBackConfigAndCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	credentials := secrets.NewMemory()
	var calls int
	service := TransactionalService{Store: Store{Path: path}, Credentials: credentials, Validator: acceptingValidator{}, Runtime: reconcilerFunc(func(context.Context) error {
		calls++
		if calls == 1 {
			return errors.New("reload failed")
		}
		return nil
	})}
	_, _, err := service.Apply(t.Context(), Mutation{Operation: "add", Source: transactionSource("warehouse"), CredentialAction: CredentialReplace, Credential: []byte("marker-secret")})
	if err == nil {
		t.Fatal("runtime failure was ignored")
	}
	document, loadErr := (Store{Path: path}).Load()
	if loadErr != nil || len(document.Sources) != 0 || calls != 2 {
		t.Fatalf("rollback = %#v, calls %d, %v", document, calls, loadErr)
	}
	active, _, _, _ := service.paths()
	if _, statErr := os.Stat(active); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("active journal remains: %v", statErr)
	}
}

func TestTransactionalServiceCommittedDeleteFailureBecomesCleanupDebt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	base := secrets.NewMemory()
	oldRef := "quackridge/source/warehouse/old"
	if err := base.Put(t.Context(), oldRef, []byte("old-secret")); err != nil {
		t.Fatal(err)
	}
	initial := Document{Version: CurrentVersion, Sources: []Source{transactionSource("warehouse")}}
	initial.Sources[0].CredentialRef = oldRef
	if err := (Store{Path: path}).Save(initial); err != nil {
		t.Fatal(err)
	}
	credentials := &faultStore{base: base, deleteErr: errors.New("keychain locked")}
	service := TransactionalService{Store: Store{Path: path}, Credentials: credentials, Validator: acceptingValidator{}}
	updated := transactionSource("warehouse")
	updated.Name = "Updated"
	document, _, err := service.Apply(t.Context(), Mutation{Operation: "update", SourceID: "warehouse", Source: updated, CredentialAction: CredentialReplace, Credential: []byte("new-secret")})
	if err != nil || document.Sources[0].Name != "Updated" || document.Sources[0].CredentialRef == oldRef {
		t.Fatalf("Apply() = %#v, %v", document, err)
	}
	_, _, _, debt := service.paths()
	entries, err := os.ReadDir(debt)
	if err != nil || len(entries) != 1 || credentials.deletedRef != oldRef {
		t.Fatalf("cleanup debt = %#v, deleted %q, %v", entries, credentials.deletedRef, err)
	}
}

func TestTransactionalServiceRemovesCredentialFromRetainedSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	credentials := secrets.NewMemory()
	oldRef := "quackridge/source/warehouse/old"
	if err := credentials.Put(t.Context(), oldRef, []byte("old-secret")); err != nil {
		t.Fatal(err)
	}
	initial := Document{Version: CurrentVersion, Sources: []Source{transactionSource("warehouse")}}
	initial.Sources[0].CredentialRef = oldRef
	if err := (Store{Path: path}).Save(initial); err != nil {
		t.Fatal(err)
	}
	service := TransactionalService{Store: Store{Path: path}, Credentials: credentials, Validator: acceptingValidator{}}
	updated := transactionSource("warehouse")
	document, _, err := service.Apply(t.Context(), Mutation{Operation: "update", SourceID: "warehouse", Source: updated, CredentialAction: CredentialRemove})
	if err != nil || document.Sources[0].CredentialRef != "" {
		t.Fatalf("document = %#v, error = %v", document, err)
	}
	if _, err := credentials.Get(t.Context(), oldRef); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("removed credential = %v", err)
	}
}

func TestRollbackRestoresPreviousCommittedHead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	service := TransactionalService{Store: Store{Path: path}}
	previous := Document{Version: CurrentVersion, Sources: []Source{}}
	candidate := Document{Version: CurrentVersion, Sources: []Source{transactionSource("warehouse")}}
	previousRevision, _ := Revision(previous)
	candidateRevision, _ := Revision(candidate)
	journal := transactionJournal{ID: "rollback-head", Phase: "prepared", Previous: previous, Candidate: candidate,
		PreviousRevision: previousRevision, CandidateRevision: candidateRevision}
	if err := service.Store.Save(candidate); err != nil {
		t.Fatal(err)
	}
	if err := service.commitSnapshot(journal); err != nil {
		t.Fatal(err)
	}
	if err := service.rollback(t.Context(), journal); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`corrupt`), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := service.Store.Load()
	if err != nil || len(recovered.Sources) != 0 {
		t.Fatalf("recovered = %#v, error = %v", recovered, err)
	}
}

func TestTransactionalServiceSerializesConcurrentOfflineMutations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	service := TransactionalService{Store: Store{Path: path}, Validator: acceptingValidator{}}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range []string{"one", "two"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, _, err := service.Apply(context.Background(), Mutation{Operation: "add", Source: transactionSource(id)})
			errs <- err
		}(id)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	document, err := (Store{Path: path}).Load()
	if err != nil || len(document.Sources) != 2 {
		t.Fatalf("document = %#v, %v", document, err)
	}
}

func TestCommittedHeadRecoversCandidateAfterPrimaryCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	service := TransactionalService{Store: Store{Path: path}, Validator: acceptingValidator{}}
	document, _, err := service.Apply(t.Context(), Mutation{Operation: "add", Source: transactionSource("warehouse")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":2,"sources":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := (Store{Path: path}).Load()
	if err != nil || len(recovered.Sources) != 1 || recovered.Sources[0].ID != document.Sources[0].ID {
		t.Fatalf("recovered = %#v, %v", recovered, err)
	}
}

func TestCorruptCommittedHeadFailsClosedWithoutBackupGuessing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	service := TransactionalService{Store: Store{Path: path}, Validator: acceptingValidator{}}
	if _, _, err := service.Apply(t.Context(), Mutation{Operation: "add", Source: transactionSource("warehouse")}); err != nil {
		t.Fatal(err)
	}
	_, _, head, _ := service.paths()
	if err := os.WriteFile(head, []byte(`{"revision":"guess"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`corrupt`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Store{Path: path}).Load(); err == nil || !strings.Contains(err.Error(), "recovery stopped") {
		t.Fatalf("error = %v", err)
	}
}

func TestCleanupDebtDrainsOnLaterMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	base := secrets.NewMemory()
	oldRef := "quackridge/source/warehouse/old"
	_ = base.Put(t.Context(), oldRef, []byte("old-secret"))
	initial := Document{Version: CurrentVersion, Sources: []Source{transactionSource("warehouse")}}
	initial.Sources[0].CredentialRef = oldRef
	if err := (Store{Path: path}).Save(initial); err != nil {
		t.Fatal(err)
	}
	failing := &faultStore{base: base, deleteErr: errors.New("locked")}
	service := TransactionalService{Store: Store{Path: path}, Credentials: failing, Validator: acceptingValidator{}}
	updated := transactionSource("warehouse")
	updated.Name = "Updated"
	if _, _, err := service.Apply(t.Context(), Mutation{Operation: "update", SourceID: "warehouse", Source: updated, CredentialAction: CredentialReplace, Credential: []byte("new-secret")}); err != nil {
		t.Fatal(err)
	}
	failing.deleteErr = nil
	if _, _, err := service.Apply(t.Context(), Mutation{Operation: "set_enabled", SourceID: "warehouse", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	_, _, _, debt := service.paths()
	entries, err := os.ReadDir(debt)
	if err != nil || len(entries) != 0 {
		t.Fatalf("debt = %#v, %v", entries, err)
	}
	if _, err := base.Get(t.Context(), oldRef); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("old credential = %v", err)
	}
}

func TestStartupGuardExcludesOfflineMutationUntilPublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	service := TransactionalService{Store: Store{Path: path}, Validator: acceptingValidator{}}
	guard, err := service.AcquireStartup(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := service.Apply(context.Background(), Mutation{Operation: "add", Source: transactionSource("warehouse")})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("mutation crossed startup guard: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("mutation did not resume")
	}
}

func TestDocumentLockExcludesSourceMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	service := TransactionalService{Store: Store{Path: path}, Validator: acceptingValidator{}}
	entered := make(chan struct{})
	release := make(chan struct{})
	lockedDone := make(chan error, 1)
	go func() {
		lockedDone <- service.WithDocumentLock(context.Background(), func(Document) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	mutationDone := make(chan error, 1)
	go func() {
		_, _, err := service.Apply(context.Background(), Mutation{Operation: "add", Source: transactionSource("warehouse")})
		mutationDone <- err
	}()
	select {
	case err := <-mutationDone:
		t.Fatalf("mutation crossed document lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-lockedDone; err != nil {
		t.Fatal(err)
	}
	if err := <-mutationDone; err != nil {
		t.Fatal(err)
	}
}

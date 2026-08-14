package config

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	quackridge "github.com/pondpilot/quackridge"
	"github.com/pondpilot/quackridge/internal/secrets"
)

type CredentialAction string

const (
	CredentialNone    CredentialAction = "none"
	CredentialKeep    CredentialAction = "keep"
	CredentialReplace CredentialAction = "replace"
	CredentialRemove  CredentialAction = "remove"
)

type Mutation struct {
	Operation        string           `json:"operation"`
	Source           Source           `json:"source"`
	SourceID         string           `json:"source_id,omitempty"`
	Enabled          bool             `json:"enabled,omitempty"`
	ExpectedRevision string           `json:"expected_revision,omitempty"`
	CredentialAction CredentialAction `json:"credential_action"`
	Credential       []byte           `json:"credential,omitempty"`
}

type Reconciler interface{ Reload(context.Context) error }

type TransactionalService struct {
	Store       Store
	Credentials secrets.Store
	Validator   Validator
	Runtime     Reconciler
	AfterLock   func(context.Context) error
}

type StartupGuard struct{ lock *fileLock }

// WithDocumentLock performs an operation against the current document while
// holding the same cross-process lock used by startup and source mutations.
func (s TransactionalService) WithDocumentLock(ctx context.Context, operation func(Document) error) error {
	if s.Store.Path == "" || operation == nil {
		return errors.New("configuration path and locked operation are required")
	}
	lock, err := acquireLock(s.Store.Path + ".lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	if s.AfterLock != nil {
		if err := s.AfterLock(ctx); err != nil {
			return err
		}
	}
	if err := s.recover(ctx); err != nil {
		return err
	}
	document, err := s.Store.Load()
	if err != nil {
		return err
	}
	return operation(document)
}

func (g *StartupGuard) Close() error {
	if g == nil || g.lock == nil {
		return nil
	}
	err := g.lock.Close()
	g.lock = nil
	return err
}

// AcquireStartup serializes recovery and initial config loading with offline
// mutations. The caller publishes its management endpoint before Close.
func (s TransactionalService) AcquireStartup(ctx context.Context) (*StartupGuard, error) {
	if s.Store.Path == "" {
		return nil, errors.New("configuration path is required")
	}
	lock, err := acquireLock(s.Store.Path + ".lock")
	if err != nil {
		return nil, err
	}
	guard := &StartupGuard{lock: lock}
	if s.AfterLock != nil {
		if err := s.AfterLock(ctx); err != nil {
			_ = guard.Close()
			return nil, err
		}
	}
	if err := s.recover(ctx); err != nil {
		_ = guard.Close()
		return nil, err
	}
	s.drainCleanupDebt(ctx, 8)
	return guard, nil
}

type transactionJournal struct {
	ID                string   `json:"id"`
	Phase             string   `json:"phase"`
	Previous          Document `json:"previous"`
	Candidate         Document `json:"candidate"`
	PreviousRevision  string   `json:"previous_revision"`
	CandidateRevision string   `json:"candidate_revision"`
	OldCredentialRef  string   `json:"old_credential_ref,omitempty"`
	NewCredentialRef  string   `json:"new_credential_ref,omitempty"`
}

type committedHead struct {
	Revision string `json:"revision"`
	Digest   string `json:"digest"`
}

func (s TransactionalService) Apply(ctx context.Context, mutation Mutation) (Document, string, error) {
	if s.Store.Path == "" {
		return Document{}, "", errors.New("configuration path is required")
	}
	lock, err := acquireLock(s.Store.Path + ".lock")
	if err != nil {
		return Document{}, "", err
	}
	defer lock.Close()
	if s.AfterLock != nil {
		if err := s.AfterLock(ctx); err != nil {
			return Document{}, "", err
		}
	}
	if err := s.recover(ctx); err != nil {
		return Document{}, "", err
	}
	s.drainCleanupDebt(ctx, 8)
	previous, err := s.Store.Load()
	if err != nil {
		return Document{}, "", err
	}
	previousRevision, err := Revision(previous)
	if err != nil {
		return Document{}, "", err
	}
	if mutation.ExpectedRevision != "" && mutation.ExpectedRevision != previousRevision {
		return Document{}, previousRevision, &quackridge.Error{Code: quackridge.CodeConflict, Message: "configuration changed; refresh and try again"}
	}
	if mutation.CredentialAction == "" {
		mutation.CredentialAction = CredentialNone
	}
	if err := validateMutationIntent(mutation); err != nil {
		return Document{}, previousRevision, &quackridge.Error{Code: quackridge.CodeValidation, Message: err.Error(), Cause: err}
	}
	candidate, oldRef, err := buildCandidate(previous, mutation)
	if err != nil {
		return Document{}, previousRevision, &quackridge.Error{Code: quackridge.CodeValidation, Message: err.Error(), Cause: err}
	}
	if err := candidate.Validate(); err != nil {
		return Document{}, previousRevision, &quackridge.Error{Code: quackridge.CodeValidation, Message: "source configuration is invalid", Cause: err}
	}
	if (mutation.Operation == "add" || mutation.Operation == "update" || mutation.Operation == "test") &&
		s.Validator != nil && mutation.CredentialAction != CredentialRemove {
		credential := mutation.Credential
		if mutation.CredentialAction == CredentialKeep && oldRef != "" && s.Credentials != nil {
			credential, err = s.Credentials.Get(ctx, oldRef)
			if err != nil {
				return Document{}, previousRevision, err
			}
			defer clear(credential)
		}
		if err := s.Validator.Validate(ctx, mutation.Source, credential); err != nil {
			return Document{}, previousRevision, &quackridge.Error{Code: quackridge.CodeSourceUnavailable, Message: "source validation failed", Cause: err}
		}
	}
	if mutation.Operation == "test" {
		return previous, previousRevision, nil
	}
	id, err := transactionID()
	if err != nil {
		return Document{}, previousRevision, err
	}
	newRef := ""
	if mutation.CredentialAction == CredentialReplace {
		if s.Credentials == nil || len(mutation.Credential) == 0 {
			return Document{}, previousRevision, errors.New("replacement credential is required")
		}
		newRef = "quackridge/source/" + mutation.Source.ID + "/" + id
		for index := range candidate.Sources {
			if candidate.Sources[index].ID == mutation.Source.ID {
				candidate.Sources[index].CredentialRef = newRef
			}
		}
	}
	if err := candidate.Validate(); err != nil {
		return Document{}, previousRevision, &quackridge.Error{Code: quackridge.CodeValidation, Message: "source configuration is invalid", Cause: err}
	}
	candidateRevision, err := Revision(candidate)
	if err != nil {
		return Document{}, previousRevision, err
	}
	journal := transactionJournal{ID: id, Phase: "prepared", Previous: previous, Candidate: candidate, PreviousRevision: previousRevision, CandidateRevision: candidateRevision, OldCredentialRef: oldRef, NewCredentialRef: newRef}
	if err := s.writeJournal(journal); err != nil {
		return Document{}, previousRevision, err
	}
	if newRef != "" {
		if err := s.Credentials.Put(ctx, newRef, mutation.Credential); err != nil {
			_ = s.removeJournal()
			return Document{}, previousRevision, err
		}
	}
	if err := s.Store.Save(candidate); err != nil {
		return Document{}, previousRevision, errors.Join(err, s.rollback(ctx, journal))
	}
	if s.Runtime != nil {
		if err := s.Runtime.Reload(ctx); err != nil {
			return Document{}, previousRevision, errors.Join(err, s.rollback(ctx, journal))
		}
	}
	if err := s.commitSnapshot(journal); err != nil {
		return Document{}, previousRevision, errors.Join(err, s.rollback(ctx, journal))
	}
	journal.Phase = "committed"
	if err := s.writeJournal(journal); err != nil {
		// The synced committed head is authoritative. Recovery will roll forward
		// and retry cleanup even if the phase rewrite could not be persisted.
		return candidate, candidateRevision, nil
	}
	if oldRef != "" && oldRef != newRef && oldRef != retainedRef(candidate, mutation.SourceID, mutation.Source.ID) {
		if s.Credentials != nil {
			if err := s.Credentials.Delete(ctx, oldRef); err != nil {
				_ = s.moveToDebt(journal)
				return candidate, candidateRevision, nil
			}
		}
	}
	_ = s.removeJournal()
	return candidate, candidateRevision, nil
}

func validateMutationIntent(mutation Mutation) error {
	switch mutation.Operation {
	case "add":
		if mutation.CredentialAction != CredentialNone && mutation.CredentialAction != CredentialReplace {
			return errors.New("add credential action must be none or replace")
		}
	case "test", "update":
		if mutation.CredentialAction != CredentialNone && mutation.CredentialAction != CredentialKeep && mutation.CredentialAction != CredentialReplace &&
			(mutation.Operation != "update" || mutation.CredentialAction != CredentialRemove) {
			return errors.New("credential action must be none, keep, replace, or remove for updates")
		}
	case "remove", "set_enabled":
		if mutation.CredentialAction != CredentialNone || len(mutation.Credential) != 0 {
			return errors.New("credential input is not allowed for this operation")
		}
	default:
		return errors.New("unsupported source mutation")
	}
	if mutation.CredentialAction != CredentialReplace && len(mutation.Credential) != 0 {
		return errors.New("credential bytes require the replace action")
	}
	return nil
}

func buildCandidate(previous Document, mutation Mutation) (Document, string, error) {
	if mutation.Operation == "add" && mutation.Source.CredentialRef != "" {
		return Document{}, "", errors.New("credential references are assigned by QuackRidge")
	}
	candidate := previous.Clone()
	index := -1
	lookup := mutation.SourceID
	if lookup == "" {
		lookup = mutation.Source.ID
	}
	for i := range candidate.Sources {
		if candidate.Sources[i].ID == lookup {
			index = i
			break
		}
	}
	oldRef := ""
	if index >= 0 {
		oldRef = candidate.Sources[index].CredentialRef
	}
	switch mutation.Operation {
	case "add":
		if index >= 0 {
			return Document{}, oldRef, errors.New("source already exists")
		}
		candidate.Sources = append(candidate.Sources, mutation.Source)
	case "test":
		if index >= 0 {
			mutation.Source.CredentialRef = oldRef
			candidate.Sources[index] = mutation.Source
		} else {
			candidate.Sources = append(candidate.Sources, mutation.Source)
		}
	case "update":
		if index < 0 {
			return Document{}, "", errors.New("source not found")
		}
		if mutation.Source.ID != lookup {
			return Document{}, oldRef, errors.New("source id cannot be changed")
		}
		mutation.Source.CredentialRef = oldRef
		if mutation.CredentialAction == CredentialRemove {
			mutation.Source.CredentialRef = ""
		}
		candidate.Sources[index] = mutation.Source
	case "remove":
		if index < 0 {
			return Document{}, "", errors.New("source not found")
		}
		candidate.Sources = append(candidate.Sources[:index], candidate.Sources[index+1:]...)
	case "set_enabled":
		if index < 0 {
			return Document{}, "", errors.New("source not found")
		}
		candidate.Sources[index].Enabled = mutation.Enabled
	default:
		return Document{}, "", errors.New("unsupported source mutation")
	}
	return candidate, oldRef, nil
}

func (s TransactionalService) paths() (string, string, string, string) {
	root := filepath.Join(filepath.Dir(s.Store.Path), "state-v2")
	return filepath.Join(root, "active-transaction.json"), filepath.Join(root, "recovery"), filepath.Join(root, "committed-head.json"), filepath.Join(root, "cleanup-debt")
}

func (s TransactionalService) writeJournal(value transactionJournal) error {
	path, _, _, _ := s.paths()
	return writeJSON(path, value)
}
func (s TransactionalService) removeJournal() error {
	path, _, _, _ := s.paths()
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
func (s TransactionalService) commitSnapshot(j transactionJournal) error {
	_, recovery, head, _ := s.paths()
	path := filepath.Join(recovery, j.CandidateRevision+".json")
	if err := writeJSON(path, j.Candidate); err != nil {
		return err
	}
	return writeJSON(head, committedHead{Revision: j.CandidateRevision, Digest: j.CandidateRevision})
}
func (s TransactionalService) rollback(ctx context.Context, j transactionJournal) error {
	var failures []error
	if err := s.Store.Save(j.Previous); err != nil {
		failures = append(failures, fmt.Errorf("restore previous configuration: %w", err))
	}
	if err := s.restorePreviousHead(j); err != nil {
		failures = append(failures, fmt.Errorf("restore previous committed head: %w", err))
	}
	if len(failures) == 0 && s.Runtime != nil {
		if err := s.Runtime.Reload(context.Background()); err != nil {
			failures = append(failures, fmt.Errorf("restore previous runtime: %w", err))
		}
	}
	if j.NewCredentialRef != "" && s.Credentials != nil {
		if err := s.Credentials.Delete(ctx, j.NewCredentialRef); err != nil && !errors.Is(err, secrets.ErrNotFound) {
			failures = append(failures, fmt.Errorf("remove staged credential: %w", err))
		}
	}
	if len(failures) != 0 {
		return errors.Join(failures...)
	}
	return s.removeJournal()
}

func (s TransactionalService) restorePreviousHead(j transactionJournal) error {
	_, recovery, head, _ := s.paths()
	revision, err := Revision(j.Previous)
	if err != nil || revision != j.PreviousRevision {
		return errors.New("previous configuration revision is invalid")
	}
	if err := writeJSON(filepath.Join(recovery, revision+".json"), j.Previous); err != nil {
		return err
	}
	return writeJSON(head, committedHead{Revision: revision, Digest: revision})
}
func (s TransactionalService) recover(ctx context.Context) error {
	path, _, _, _ := s.paths()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var j transactionJournal
	if err := json.Unmarshal(data, &j); err != nil {
		return errors.New("transaction recovery journal is invalid")
	}
	if j.Phase == "committed" {
		return s.moveToDebt(j)
	}
	_, _, headPath, _ := s.paths()
	var head committedHead
	if data, headErr := os.ReadFile(headPath); headErr == nil && json.Unmarshal(data, &head) == nil && head.Revision == j.CandidateRevision && head.Digest == j.CandidateRevision {
		j.Phase = "committed"
		if err := s.writeJournal(j); err != nil {
			return errors.New("committed transaction recovery could not be recorded")
		}
		return s.moveToDebt(j)
	}
	if err := s.rollback(ctx, j); err != nil {
		return fmt.Errorf("transaction rollback could not be proven: %w", err)
	}
	return nil
}

func (s TransactionalService) drainCleanupDebt(ctx context.Context, limit int) {
	if s.Credentials == nil || limit <= 0 {
		return
	}
	_, _, _, debt := s.paths()
	entries, err := os.ReadDir(debt)
	if err != nil {
		return
	}
	document, err := s.Store.Load()
	if err != nil {
		return
	}
	for _, entry := range entries {
		if limit == 0 || entry.IsDir() {
			break
		}
		data, err := os.ReadFile(filepath.Join(debt, entry.Name()))
		if err != nil {
			continue
		}
		var journal transactionJournal
		if json.Unmarshal(data, &journal) != nil || journal.Phase != "committed" {
			continue
		}
		if journal.OldCredentialRef != "" && retainedCredential(document, journal.OldCredentialRef) {
			continue
		}
		if journal.OldCredentialRef != "" {
			err = s.Credentials.Delete(ctx, journal.OldCredentialRef)
			if err != nil && !errors.Is(err, secrets.ErrNotFound) {
				continue
			}
		}
		if os.Remove(filepath.Join(debt, entry.Name())) == nil {
			limit--
		}
	}
	_ = syncDirectory(debt)
}
func (s TransactionalService) moveToDebt(j transactionJournal) error {
	active, _, _, debt := s.paths()
	if err := os.MkdirAll(debt, 0o700); err != nil {
		return err
	}
	target := filepath.Join(debt, j.ID+".json")
	if _, err := os.Stat(target); err == nil {
		return s.removeJournal()
	}
	if err := os.Rename(active, target); err != nil {
		return err
	}
	return errors.Join(syncDirectory(filepath.Dir(active)), syncDirectory(debt))
}

func writeJSON(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeAtomic(path, encoded)
}
func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func transactionID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
func retainedRef(document Document, ids ...string) string {
	for _, source := range document.Sources {
		for _, id := range ids {
			if source.ID == id {
				return source.CredentialRef
			}
		}
	}
	return ""
}

func retainedCredential(document Document, reference string) bool {
	for _, source := range document.Sources {
		if source.CredentialRef == reference {
			return true
		}
	}
	return false
}
func errorsJoin(values ...error) error { return errors.Join(values...) }

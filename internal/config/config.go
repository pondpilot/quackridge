// Package config persists versioned, non-secret source configuration.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pondpilot/quackridge/internal/source"
)

const CurrentVersion = 1

type Document struct {
	Version int      `json:"version"`
	Sources []Source `json:"sources"`
}

type Source struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Alias         string          `json:"alias"`
	Type          string          `json:"type"`
	Enabled       bool            `json:"enabled"`
	CredentialRef string          `json:"credential_ref"`
	Options       json.RawMessage `json:"options"`
}

func (d Document) Clone() Document {
	clone := d
	clone.Sources = make([]Source, len(d.Sources))
	for index, configured := range d.Sources {
		clone.Sources[index] = configured
		clone.Sources[index].Options = bytes.Clone(configured.Options)
	}
	return clone
}

func (d Document) Validate() error {
	if d.Version != CurrentVersion {
		return fmt.Errorf("unsupported configuration version %d", d.Version)
	}
	ids := make(map[string]struct{}, len(d.Sources))
	aliases := make(map[string]struct{}, len(d.Sources))
	credentialRefs := make(map[string]struct{}, len(d.Sources))
	for _, configured := range d.Sources {
		if configured.ID == "" || configured.Name == "" || configured.Type == "" || configured.CredentialRef == "" {
			return errors.New("source id, name, type, and credential reference are required")
		}
		if err := source.ValidateAlias(configured.Alias); err != nil {
			return err
		}
		if _, exists := ids[configured.ID]; exists {
			return fmt.Errorf("duplicate source id %q", configured.ID)
		}
		if _, exists := aliases[configured.Alias]; exists {
			return fmt.Errorf("duplicate source alias %q", configured.Alias)
		}
		if _, exists := credentialRefs[configured.CredentialRef]; exists {
			return fmt.Errorf("duplicate credential reference %q", configured.CredentialRef)
		}
		ids[configured.ID] = struct{}{}
		aliases[configured.Alias] = struct{}{}
		credentialRefs[configured.CredentialRef] = struct{}{}
		if err := validateOptions(configured.Options); err != nil {
			return fmt.Errorf("source %q options: %w", configured.ID, err)
		}
	}
	return nil
}

func validateOptions(raw json.RawMessage) error {
	if len(raw) == 0 {
		return errors.New("options are required")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return errors.New("options must be an object")
	}
	if containsSecretField(object) {
		return errors.New("secret-bearing fields are forbidden")
	}
	return nil
}

func containsSecretField(value any) bool {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
			if slices.Contains([]string{"password", "passwd", "secret", "token", "dsn", "connection_string", "uri"}, normalized) {
				return true
			}
			if containsSecretField(child) {
				return true
			}
		}
	case []any:
		for _, child := range current {
			if containsSecretField(child) {
				return true
			}
		}
	}
	return false
}

type Store struct{ Path string }

func DefaultPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "QuackRidge", "config.json"), nil
}

func (s Store) Load() (Document, error) {
	document, err := loadFile(s.Path)
	if err == nil {
		migrated, migrationErr := migrate(document)
		if migrationErr != nil {
			return Document{}, migrationErr
		}
		if document.Version != CurrentVersion {
			if err := s.Save(migrated); err != nil {
				return Document{}, fmt.Errorf("persist configuration migration: %w", err)
			}
		}
		return migrated, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return Document{Version: CurrentVersion, Sources: []Source{}}, nil
	}
	backup, backupErr := loadFile(s.Path + ".bak")
	if backupErr != nil {
		return Document{}, fmt.Errorf("load configuration: %w", err)
	}
	recovered, migrationErr := migrate(backup)
	if migrationErr != nil {
		return Document{}, migrationErr
	}
	return recovered, nil
}

func loadFile(path string) (Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Document{}, err
	}
	var document Document
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return Document{}, err
	}
	return document, nil
}

func migrate(document Document) (Document, error) {
	switch document.Version {
	case 0:
		document.Version = CurrentVersion
	case CurrentVersion:
	default:
		return Document{}, fmt.Errorf("unsupported configuration version %d", document.Version)
	}
	if document.Sources == nil {
		document.Sources = []Source{}
	}
	if err := document.Validate(); err != nil {
		return Document{}, err
	}
	return document.Clone(), nil
}

func (s Store) Save(document Document) error {
	if err := document.Validate(); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	if existing, err := os.ReadFile(s.Path); err == nil {
		if err := writeAtomic(s.Path+".bak", existing); err != nil {
			return fmt.Errorf("backup configuration: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := writeAtomic(s.Path, encoded); err != nil {
		return fmt.Errorf("save configuration: %w", err)
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".quackridge-config-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

package config

import (
	"context"
	"errors"
	"fmt"

	"github.com/pondpilot/quackridge/internal/secrets"
)

type Validator interface {
	Validate(context.Context, Source, []byte) error
}

type Service struct {
	Store       Store
	Credentials secrets.Store
	Validator   Validator
}

func (s Service) Add(ctx context.Context, configured Source, credential []byte) error {
	if s.Credentials == nil || s.Validator == nil {
		return errors.New("credential store and source validator are required")
	}
	document, err := s.Store.Load()
	if err != nil {
		return err
	}
	candidate := document.Clone()
	candidate.Sources = append(candidate.Sources, configured)
	if err := candidate.Validate(); err != nil {
		return err
	}
	if err := s.Validator.Validate(ctx, configured, credential); err != nil {
		return fmt.Errorf("validate source: %w", err)
	}
	if err := s.Credentials.Put(ctx, configured.CredentialRef, credential); err != nil {
		return fmt.Errorf("store source credential: %w", err)
	}
	if err := s.Store.Save(candidate); err != nil {
		_ = s.Credentials.Delete(context.Background(), configured.CredentialRef)
		return err
	}
	return nil
}

func (s Service) Test(ctx context.Context, configured Source, credential []byte) error {
	if s.Validator == nil {
		return errors.New("source validator is required")
	}
	document, err := s.Store.Load()
	if err != nil {
		return err
	}
	candidate := document.Clone()
	candidate.Sources = append(candidate.Sources, configured)
	if err := candidate.Validate(); err != nil {
		return err
	}
	return s.Validator.Validate(ctx, configured, credential)
}

func (s Service) Remove(ctx context.Context, sourceID string) error {
	if s.Credentials == nil {
		return errors.New("credential store is required")
	}
	document, err := s.Store.Load()
	if err != nil {
		return err
	}
	index := -1
	for current, configured := range document.Sources {
		if configured.ID == sourceID {
			index = current
			break
		}
	}
	if index < 0 {
		return errors.New("source not found")
	}
	removed := document.Sources[index]
	candidate := document.Clone()
	candidate.Sources = append(candidate.Sources[:index], candidate.Sources[index+1:]...)
	if err := s.Store.Save(candidate); err != nil {
		return err
	}
	if err := s.Credentials.Delete(ctx, removed.CredentialRef); err != nil {
		_ = s.Store.Save(document)
		return fmt.Errorf("delete source credential: %w", err)
	}
	return nil
}

//go:build linux

package secrets

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
)

type systemStore struct{ command string }

func NewSystemStore() (Store, error) {
	command, err := exec.LookPath("secret-tool")
	if err != nil {
		return nil, errors.New("Secret Service client is unavailable")
	}
	return &systemStore{command: command}, nil
}

func (s *systemStore) Get(ctx context.Context, reference string) ([]byte, error) {
	if reference == "" {
		return nil, ErrNotFound
	}
	output, err := exec.CommandContext(ctx, s.command, "lookup", "service", "quackridge", "account", reference).Output()
	if err != nil {
		return nil, ErrNotFound
	}
	value := bytes.TrimSuffix(output, []byte{'\n'})
	if len(value) == 0 {
		return nil, ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *systemStore) Put(ctx context.Context, reference string, value []byte) error {
	if reference == "" || len(value) == 0 {
		return errors.New("credential reference and value are required")
	}
	command := exec.CommandContext(ctx, s.command, "store", "--label=QuackRidge", "service", "quackridge", "account", reference)
	command.Stdin = bytes.NewReader(value)
	if err := command.Run(); err != nil {
		return errors.New("store credential in Secret Service")
	}
	return nil
}

func (s *systemStore) Delete(ctx context.Context, reference string) error {
	if strings.TrimSpace(reference) == "" {
		return ErrNotFound
	}
	if err := exec.CommandContext(ctx, s.command, "clear", "service", "quackridge", "account", reference).Run(); err != nil {
		return ErrNotFound
	}
	return nil
}

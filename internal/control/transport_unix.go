//go:build !windows

package control

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"time"
)

func DefaultAddress() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "QuackRidge", "control.sock"), nil
}

func listen(address string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(address), 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(address); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("control address exists and is not a socket")
		}
		probe, dialErr := net.DialTimeout("unix", address, 250*time.Millisecond)
		if dialErr == nil {
			_ = probe.Close()
			return nil, fmt.Errorf("control address is already in use")
		}
		if err := os.Remove(address); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", address)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(address, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	identity, err := os.Lstat(address)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	return &cleanupListener{Listener: listener, path: address, identity: identity}, nil
}

type cleanupListener struct {
	net.Listener
	path     string
	identity os.FileInfo
}

func (l *cleanupListener) Close() error {
	err := l.Listener.Close()
	removeErr := error(nil)
	if current, statErr := os.Lstat(l.path); statErr == nil && os.SameFile(l.identity, current) {
		removeErr = os.Remove(l.path)
	} else if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		removeErr = statErr
	}
	if errors.Is(removeErr, fs.ErrNotExist) {
		removeErr = nil
	}
	return errors.Join(err, removeErr)
}

func dial(ctx context.Context, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", address)
}

func EndpointPresent(address string) bool {
	_, err := os.Lstat(address)
	return err == nil
}

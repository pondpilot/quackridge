//go:build !windows

package config

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type fileLock struct{ file *os.File }

func acquireLock(path string) (*fileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &fileLock{file: file}, nil
}

func (l *fileLock) Close() error {
	err := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	return errorsJoin(err, l.file.Close())
}

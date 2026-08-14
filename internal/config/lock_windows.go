//go:build windows

package config

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

type fileLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func acquireLock(path string) (*fileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	lock := &fileLock{file: file}
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &lock.overlapped); err != nil {
		_ = file.Close()
		return nil, err
	}
	return lock, nil
}

func (l *fileLock) Close() error {
	err := windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, &l.overlapped)
	if errors.Is(err, windows.ERROR_NOT_LOCKED) {
		err = nil
	}
	return errorsJoin(err, l.file.Close())
}

//go:build windows

package secrets

import (
	"context"
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	credentialTypeGeneric         = 1
	credentialPersistLocalMachine = 2
)

var (
	advapi32       = windows.NewLazySystemDLL("advapi32.dll")
	procCredRead   = advapi32.NewProc("CredReadW")
	procCredWrite  = advapi32.NewProc("CredWriteW")
	procCredDelete = advapi32.NewProc("CredDeleteW")
	procCredFree   = advapi32.NewProc("CredFree")
)

type windowsCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

type systemStore struct{}

func NewSystemStore() (Store, error) { return &systemStore{}, nil }

func credentialTarget(reference string) (*uint16, error) {
	if reference == "" {
		return nil, ErrNotFound
	}
	return windows.UTF16PtrFromString("QuackRidge/" + reference)
}

func (*systemStore) Get(_ context.Context, reference string) ([]byte, error) {
	target, err := credentialTarget(reference)
	if err != nil {
		return nil, err
	}
	var credential *windowsCredential
	result, _, callErr := procCredRead.Call(uintptr(unsafe.Pointer(target)), credentialTypeGeneric, 0, uintptr(unsafe.Pointer(&credential)))
	if result == 0 {
		if errors.Is(callErr, syscall.ERROR_NOT_FOUND) {
			return nil, ErrNotFound
		}
		return nil, errors.New("read credential from Windows Credential Manager")
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(credential)))
	if credential.CredentialBlobSize == 0 {
		return nil, ErrNotFound
	}
	return append([]byte(nil), unsafe.Slice(credential.CredentialBlob, credential.CredentialBlobSize)...), nil
}

func (*systemStore) Put(_ context.Context, reference string, value []byte) error {
	if len(value) == 0 {
		return errors.New("credential reference and value are required")
	}
	target, err := credentialTarget(reference)
	if err != nil {
		return err
	}
	copyValue := append([]byte(nil), value...)
	defer clear(copyValue)
	credential := windowsCredential{
		Type: credentialTypeGeneric, TargetName: target,
		CredentialBlobSize: uint32(len(copyValue)), CredentialBlob: &copyValue[0],
		Persist: credentialPersistLocalMachine,
	}
	result, _, _ := procCredWrite.Call(uintptr(unsafe.Pointer(&credential)), 0)
	if result == 0 {
		return errors.New("store credential in Windows Credential Manager")
	}
	return nil
}

func (*systemStore) Delete(_ context.Context, reference string) error {
	target, err := credentialTarget(reference)
	if err != nil {
		return err
	}
	result, _, callErr := procCredDelete.Call(uintptr(unsafe.Pointer(target)), credentialTypeGeneric, 0)
	if result == 0 {
		if errors.Is(callErr, syscall.ERROR_NOT_FOUND) {
			return ErrNotFound
		}
		return errors.New("delete credential from Windows Credential Manager")
	}
	return nil
}

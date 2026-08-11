//go:build darwin && cgo

package secrets

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <stdlib.h>
#include <string.h>

static const char *qr_service = "io.pondpilot.quackridge";

static OSStatus qr_find(const char *account, void **data, UInt32 *length, SecKeychainItemRef *item) {
	return SecKeychainFindGenericPassword(NULL, (UInt32)strlen(qr_service), qr_service,
		(UInt32)strlen(account), account, length, data, item);
}

static OSStatus qr_put(const char *account, const void *data, UInt32 length) {
	SecKeychainItemRef item = NULL;
	OSStatus status = SecKeychainFindGenericPassword(NULL, (UInt32)strlen(qr_service), qr_service,
		(UInt32)strlen(account), account, NULL, NULL, &item);
	if (status == errSecSuccess) {
		status = SecKeychainItemModifyAttributesAndData(item, NULL, length, data);
		CFRelease(item);
		return status;
	}
	if (status != errSecItemNotFound) return status;
	return SecKeychainAddGenericPassword(NULL, (UInt32)strlen(qr_service), qr_service,
		(UInt32)strlen(account), account, length, data, NULL);
}

static OSStatus qr_delete(const char *account) {
	SecKeychainItemRef item = NULL;
	OSStatus status = SecKeychainFindGenericPassword(NULL, (UInt32)strlen(qr_service), qr_service,
		(UInt32)strlen(account), account, NULL, NULL, &item);
	if (status != errSecSuccess) return status;
	status = SecKeychainItemDelete(item);
	CFRelease(item);
	return status;
}

static void qr_zero(void *data, size_t length) {
	if (data != NULL) memset(data, 0, length);
}
*/
import "C"

import (
	"context"
	"errors"
	"unsafe"
)

type systemStore struct{}

func NewSystemStore() (Store, error) { return &systemStore{}, nil }

func (*systemStore) Get(_ context.Context, reference string) ([]byte, error) {
	if reference == "" {
		return nil, ErrNotFound
	}
	account := C.CString(reference)
	defer C.free(unsafe.Pointer(account))
	var data unsafe.Pointer
	var length C.UInt32
	status := C.qr_find(account, &data, &length, nil)
	if status != C.errSecSuccess {
		return nil, ErrNotFound
	}
	defer C.SecKeychainItemFreeContent(nil, data)
	return C.GoBytes(data, C.int(length)), nil
}

func (*systemStore) Put(_ context.Context, reference string, value []byte) error {
	if reference == "" || len(value) == 0 {
		return errors.New("credential reference and value are required")
	}
	account := C.CString(reference)
	defer C.free(unsafe.Pointer(account))
	data := C.CBytes(value)
	defer func() {
		C.qr_zero(data, C.size_t(len(value)))
		C.free(data)
	}()
	if status := C.qr_put(account, data, C.UInt32(len(value))); status != C.errSecSuccess {
		return errors.New("store credential in Keychain")
	}
	return nil
}

func (*systemStore) Delete(_ context.Context, reference string) error {
	if reference == "" {
		return ErrNotFound
	}
	account := C.CString(reference)
	defer C.free(unsafe.Pointer(account))
	if status := C.qr_delete(account); status != C.errSecSuccess {
		return ErrNotFound
	}
	return nil
}

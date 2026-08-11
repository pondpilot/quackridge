//go:build darwin && !cgo

package secrets

import "errors"

func NewSystemStore() (Store, error) {
	return nil, errors.New("macOS Keychain requires CGO")
}

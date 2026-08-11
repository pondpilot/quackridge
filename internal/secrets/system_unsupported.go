//go:build !linux && !darwin && !windows

package secrets

import "errors"

func NewSystemStore() (Store, error) {
	return nil, errors.New("platform credential store is unsupported")
}

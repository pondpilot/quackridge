package reconcile

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/pondpilot/quackridge/internal/config"
	"github.com/pondpilot/quackridge/internal/source"
	"github.com/pondpilot/quackridge/internal/source/postgres"
)

type PostgresFactory struct{ Attacher postgres.Attacher }

func (PostgresFactory) Type() string { return "postgres" }

func (f PostgresFactory) Build(configured config.Source, credential []byte) (source.Adapter, error) {
	var options postgres.Config
	decoder := json.NewDecoder(bytes.NewReader(configured.Options))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&options); err != nil {
		return nil, err
	}
	if len(credential) == 0 {
		return nil, errors.New("PostgreSQL credential is empty")
	}
	return postgres.New(f.Attacher, options, postgres.Credential{Password: string(credential)}), nil
}

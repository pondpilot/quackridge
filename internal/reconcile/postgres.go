package reconcile

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/pondpilot/quackridge/internal/config"
	"github.com/pondpilot/quackridge/internal/source"
	"github.com/pondpilot/quackridge/internal/source/postgres"
)

type RootCertificateResolver interface{ Resolve(string) (string, error) }

type PostgresFactory struct {
	Attacher     postgres.Attacher
	Certificates RootCertificateResolver
}

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
	rootPath := ""
	if options.RootCertRef != "" {
		if f.Certificates == nil {
			return nil, errors.New("managed root certificate store is unavailable")
		}
		resolved, err := f.Certificates.Resolve(options.RootCertRef)
		if err != nil {
			return nil, err
		}
		rootPath = resolved
	}
	return postgres.New(f.Attacher, options, postgres.Credential{Password: string(credential), RootCertificatePath: rootPath}), nil
}

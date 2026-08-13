package reconcile

import (
	"bytes"
	"encoding/json"

	"github.com/pondpilot/quackridge/internal/config"
	"github.com/pondpilot/quackridge/internal/source"
	"github.com/pondpilot/quackridge/internal/source/filedb"
	"github.com/pondpilot/quackridge/internal/source/mysql"
	"github.com/pondpilot/quackridge/internal/source/odbc"
)

type MySQLFactory struct{ Attacher mysql.Attacher }

func (MySQLFactory) Type() string { return "mysql" }

func (f MySQLFactory) Build(configured config.Source, credential []byte) (source.Adapter, error) {
	var options mysql.Config
	decoder := json.NewDecoder(bytes.NewReader(configured.Options))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&options); err != nil {
		return nil, err
	}
	return mysql.New(f.Attacher, options, mysql.Credential{Password: string(credential)}), nil
}

type ODBCFactory struct{ Attacher odbc.Attacher }

func (ODBCFactory) Type() string { return "odbc" }

func (f ODBCFactory) Build(configured config.Source, credential []byte) (source.Adapter, error) {
	var options odbc.Config
	decoder := json.NewDecoder(bytes.NewReader(configured.Options))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&options); err != nil {
		return nil, err
	}
	credentialValue, err := odbc.DecodeCredential(credential)
	if err != nil {
		return nil, err
	}
	return odbc.New(f.Attacher, options, credentialValue), nil
}

type FileFactory struct {
	Connector string
	Attacher  filedb.Attacher
}

func (f FileFactory) Type() string { return f.Connector }

func (f FileFactory) Build(configured config.Source, _ []byte) (source.Adapter, error) {
	var options filedb.Config
	decoder := json.NewDecoder(bytes.NewReader(configured.Options))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&options); err != nil {
		return nil, err
	}
	return filedb.New(f.Attacher, f.Connector, options), nil
}

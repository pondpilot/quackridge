package mysql

import (
	"net"
	"testing"

	"github.com/pondpilot/quackridge/internal/source"
)

func TestValidateConnectorConfiguration(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	adapter := New(nil, Config{Host: "127.0.0.1", Port: port, Database: "commerce", User: "reader", SSLMode: "preferred"}, Credential{})
	definition := source.Definition{ID: "commerce", Name: "Commerce", Alias: "commerce", ConnectorType: "mysql", DatabaseType: "mysql", Enabled: true}
	if err := adapter.Validate(t.Context(), definition); err != nil {
		t.Fatal(err)
	}
	adapter.config.SSLMode = "unsafe"
	if err := adapter.Validate(t.Context(), definition); err == nil {
		t.Fatal("unsafe SSL mode accepted")
	}
}

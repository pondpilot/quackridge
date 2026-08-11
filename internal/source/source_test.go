package source

import (
	"context"
	"testing"
)

func TestValidateAlias(t *testing.T) {
	for _, valid := range []string{"warehouse", "pg_1", "a"} {
		if err := ValidateAlias(valid); err != nil {
			t.Errorf("%q: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "1pg", "has-dash", `x"; ATTACH 'evil'`} {
		if err := ValidateAlias(invalid); err == nil {
			t.Errorf("expected %q to fail", invalid)
		}
	}
}

type testAdapter struct{ sourceType string }

func (a testAdapter) Type() string                                              { return a.sourceType }
func (testAdapter) Validate(context.Context, Definition) error                  { return nil }
func (testAdapter) Attach(context.Context, Definition) error                    { return nil }
func (testAdapter) Metadata(context.Context, Definition) ([]MetadataRow, error) { return nil, nil }
func (testAdapter) Health(context.Context, Definition) error                    { return nil }
func (testAdapter) Cleanup(context.Context, Definition) error                   { return nil }

func TestRegistryOwnsAdapterLifecycleContract(t *testing.T) {
	registry, err := NewRegistry(testAdapter{sourceType: "postgres"}, testAdapter{sourceType: "mysql"})
	if err != nil {
		t.Fatal(err)
	}
	if got := registry.Types(); len(got) != 2 || got[0] != "mysql" || got[1] != "postgres" {
		t.Fatalf("types = %v", got)
	}
	if adapter, ok := registry.Adapter("postgres"); !ok || adapter.Type() != "postgres" {
		t.Fatalf("postgres adapter = %v, %v", adapter, ok)
	}
	if _, err := NewRegistry(testAdapter{sourceType: "postgres"}, testAdapter{sourceType: "postgres"}); err == nil {
		t.Fatal("duplicate adapter was accepted")
	}
}

package source

import "testing"

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

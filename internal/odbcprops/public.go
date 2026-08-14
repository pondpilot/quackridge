// Package odbcprops defines the reviewed non-secret ODBC configuration surface.
package odbcprops

import "strings"

func PublicAllowed(databaseType, key string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(key))
	common := map[string]bool{"APPLICATIONNAME": true, "CONNECTTIMEOUT": true, "QUERYTIMEOUT": true, "READONLY": true}
	if common[normalized] {
		return true
	}
	allowed := map[string]map[string]bool{
		"postgres": {"SSLMODE": true},
		"mysql":    {"CHARSET": true, "TLSVERSION": true}, "mariadb": {"CHARSET": true, "TLSVERSION": true},
		"sqlserver": {"SERVER": true, "ENCRYPT": true, "TRUSTSERVERCERTIFICATE": true, "DATABASE": true},
		"oracle":    {"DBQ": true}, "sqlite": {"TIMEOUT": true}, "duckdb": {"ACCESS_MODE": true}, "odbc": {},
	}
	return allowed[strings.ToLower(databaseType)][normalized]
}

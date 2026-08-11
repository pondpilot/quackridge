package main

import (
	"encoding/json"
	"fmt"
	"os"

	quackridge "github.com/pondpilot/quackridge"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"product":          quackridge.Product,
			"product_version":  quackridge.Version,
			"protocol_version": quackridge.ProtocolVersion,
			"duckdb_version":   quackridge.DuckDBVersion,
		})
		return
	}
	fmt.Fprintln(os.Stderr, "usage: quackridge version")
	os.Exit(2)
}

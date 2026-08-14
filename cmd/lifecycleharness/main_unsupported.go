//go:build !darwin

package main

import (
	"fmt"
	"os"
)

func main() { fmt.Fprintln(os.Stderr, "lifecycle harness requires macOS"); os.Exit(2) }

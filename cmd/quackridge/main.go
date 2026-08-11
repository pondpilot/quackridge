package main

import (
	"os"

	"github.com/pondpilot/quackridge/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}

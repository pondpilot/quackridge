package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	quackridge "github.com/pondpilot/quackridge"
)

type manifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type manifest struct {
	SchemaVersion             int            `json:"schema_version"`
	ProductVersion            string         `json:"product_version"`
	ManagementProtocolVersion int            `json:"management_protocol_version"`
	MinimumOS                 string         `json:"minimum_os"`
	Architectures             []string       `json:"architectures"`
	Helper                    manifestFile   `json:"helper"`
	Extensions                []manifestFile `json:"extensions"`
}

func main() {
	root := flag.String("root", "", "app Contents directory")
	helper := flag.String("helper", "Helpers/quackridge", "helper path relative to root")
	extensions := flag.String("extensions", "Resources/Backend/extensions", "extension directory relative to root")
	output := flag.String("output", "", "manifest output path")
	architecture := flag.String("architecture", "", "arm64 or amd64")
	version := flag.String("version", quackridge.Version, "product version")
	flag.Parse()
	if *root == "" || *output == "" || (*architecture != "arm64" && *architecture != "amd64") {
		fatal("root, output, and architecture are required")
	}
	helperFile, err := inspect(*root, *helper)
	if err != nil {
		fatal(err.Error())
	}
	entries, err := filepath.Glob(filepath.Join(*root, *extensions, "*.duckdb_extension"))
	if err != nil {
		fatal(err.Error())
	}
	files := make([]manifestFile, 0, len(entries))
	for _, path := range entries {
		relative, _ := filepath.Rel(*root, path)
		value, err := inspect(*root, relative)
		if err != nil {
			fatal(err.Error())
		}
		files = append(files, value)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	if len(files) == 0 {
		fatal("no extensions found")
	}
	value := manifest{SchemaVersion: 1, ProductVersion: *version, ManagementProtocolVersion: 2, MinimumOS: "13.0", Architectures: []string{*architecture}, Helper: helperFile, Extensions: files}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fatal(err.Error())
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(*output, encoded, 0o644); err != nil {
		fatal(err.Error())
	}
}

func inspect(root, relative string) (manifestFile, error) {
	if filepath.IsAbs(relative) || strings.HasPrefix(filepath.Clean(relative), "..") {
		return manifestFile{}, fmt.Errorf("unsafe manifest path %q", relative)
	}
	path := filepath.Join(root, relative)
	file, err := os.Open(path)
	if err != nil {
		return manifestFile{}, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return manifestFile{}, err
	}
	return manifestFile{Path: filepath.ToSlash(relative), SHA256: hex.EncodeToString(hash.Sum(nil)), Size: size}, nil
}
func fatal(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }

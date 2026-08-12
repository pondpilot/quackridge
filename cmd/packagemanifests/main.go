// Command packagemanifests emits Homebrew and WinGet definitions from the
// validated release manifest so package URLs and hashes cannot drift.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	protocol "github.com/pondpilot/quackridge/protocol/v1"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("packagemanifests", flag.ContinueOnError)
	manifestPath := flags.String("manifest", "dist/release-manifest.json", "release manifest")
	output := flags.String("output", "dist", "output directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	data, err := os.ReadFile(*manifestPath)
	if err != nil {
		return err
	}
	var manifest protocol.ReleaseManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return err
	}
	if err := protocol.ValidateReleaseManifest(manifest); err != nil {
		return err
	}
	assets := make(map[string]protocol.ReleaseAsset)
	for _, asset := range manifest.Assets {
		assets[asset.OS+"/"+asset.Arch] = asset
	}
	values := map[string]any{"Version": manifest.Version, "DarwinAMD64": assets["darwin/amd64"], "DarwinARM64": assets["darwin/arm64"], "WindowsAMD64": assets["windows/amd64"]}
	files := []struct {
		name, body string
	}{
		{"quackridge.rb", homebrewTemplate},
		{"PondPilot.QuackRidge.yaml", wingetVersionTemplate},
		{"PondPilot.QuackRidge.installer.yaml", wingetInstallerTemplate},
		{"PondPilot.QuackRidge.locale.en-US.yaml", wingetLocaleTemplate},
	}
	if err := os.MkdirAll(*output, 0o755); err != nil {
		return err
	}
	for _, file := range files {
		tmpl, err := template.New(file.name).Parse(file.body)
		if err != nil {
			return err
		}
		var rendered bytes.Buffer
		if err := tmpl.Execute(&rendered, values); err != nil {
			return err
		}
		if rendered.Len() == 0 {
			return errors.New("generated package manifest is empty")
		}
		if err := os.WriteFile(filepath.Join(*output, file.name), rendered.Bytes(), 0o644); err != nil {
			return err
		}
	}
	return nil
}

const homebrewTemplate = `cask "quackridge" do
  version "{{.Version}}"

  on_arm do
    sha256 "{{.DarwinARM64.SHA256}}"
    url "{{.DarwinARM64.URL}}"
  end

  on_intel do
    sha256 "{{.DarwinAMD64.SHA256}}"
    url "{{.DarwinAMD64.URL}}"
  end

  name "QuackRidge"
  desc "Experimental local read-only PostgreSQL bridge for PondPilot"
  homepage "https://github.com/pondpilot/quackridge"

  binary "quackridge_#{version}_darwin_#{Hardware::CPU.arm? ? "arm64" : "amd64"}/quackridge"

  caveats "QuackRidge is experimental and requires an explicitly read-only PostgreSQL role."
end
`

const wingetVersionTemplate = `PackageIdentifier: PondPilot.QuackRidge
PackageVersion: {{.Version}}
DefaultLocale: en-US
ManifestType: version
ManifestVersion: 1.9.0
`

const wingetInstallerTemplate = `PackageIdentifier: PondPilot.QuackRidge
PackageVersion: {{.Version}}
InstallerType: zip
NestedInstallerType: portable
NestedInstallerFiles:
  - RelativeFilePath: quackridge_{{.Version}}_windows_amd64\quackridge.exe
    PortableCommandAlias: quackridge
Installers:
  - Architecture: x64
    InstallerUrl: {{.WindowsAMD64.URL}}
    InstallerSha256: {{.WindowsAMD64.SHA256}}
ManifestType: installer
ManifestVersion: 1.9.0
`

const wingetLocaleTemplate = `PackageIdentifier: PondPilot.QuackRidge
PackageVersion: {{.Version}}
PackageLocale: en-US
Publisher: PondPilot
PackageName: QuackRidge
License: Apache-2.0
ShortDescription: Experimental local read-only PostgreSQL bridge for PondPilot
PackageUrl: https://github.com/pondpilot/quackridge
LicenseUrl: https://github.com/pondpilot/quackridge/blob/main/LICENSE
ManifestType: defaultLocale
ManifestVersion: 1.9.0
`

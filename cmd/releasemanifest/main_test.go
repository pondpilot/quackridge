package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	protocol "github.com/pondpilot/quackridge/protocol/v1"
)

func TestVerifyManifestHashesArchiveContents(t *testing.T) {
	directory := t.TempDir()
	manifest := protocol.ReleaseManifest{
		Version: "0.1.0", Channel: "prerelease",
		Protocol: protocol.ProtocolRange{Minimum: 1, Maximum: 1},
	}
	targets := []struct{ os, arch, name string }{
		{"darwin", "amd64", "quackridge_0.1.0_darwin_amd64.tar.gz"},
		{"darwin", "arm64", "quackridge_0.1.0_darwin_arm64.tar.gz"},
		{"linux", "amd64", "quackridge_0.1.0_linux_amd64.tar.gz"},
	}
	for _, target := range targets {
		contents := []byte("archive-" + target.name)
		digest := sha256.Sum256(contents)
		digestText := hex.EncodeToString(digest[:])
		if err := os.WriteFile(filepath.Join(directory, target.name), contents, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, target.name+".sha256"), []byte(digestText+"  "+target.name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, target.name+".sigstore.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		manifest.Assets = append(manifest.Assets, protocol.ReleaseAsset{
			OS: target.os, Arch: target.arch,
			URL:       "https://github.com/pondpilot/quackridge/releases/download/v0.1.0/" + target.name,
			SHA256:    digestText,
			Signature: "https://github.com/pondpilot/quackridge/releases/download/v0.1.0/" + target.name + ".sigstore.json",
			MinimumOS: "test",
		})
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "release-manifest.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(directory); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, targets[0].name), []byte("corrupted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(directory); err == nil {
		t.Fatal("corrupted release archive accepted")
	}
}

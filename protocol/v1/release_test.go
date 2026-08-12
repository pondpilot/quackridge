package v1_test

import (
	"strings"
	"testing"

	protocol "github.com/pondpilot/quackridge/protocol/v1"
)

func TestValidateReleaseManifest(t *testing.T) {
	manifest := protocol.ReleaseManifest{
		Version: "0.1.0-rc.1", Channel: "prerelease",
		Protocol: protocol.ProtocolRange{Minimum: 1, Maximum: 1},
	}
	for _, target := range []struct{ os, arch string }{{"darwin", "amd64"}, {"darwin", "arm64"}, {"linux", "amd64"}, {"windows", "amd64"}} {
		manifest.Assets = append(manifest.Assets, protocol.ReleaseAsset{
			OS: target.os, Arch: target.arch, URL: "https://example.test/quackridge-" + target.os + "-" + target.arch,
			SHA256: strings.Repeat("a", 64), Signature: "https://example.test/asset.sigstore.json", MinimumOS: "test",
		})
	}
	if err := protocol.ValidateReleaseManifest(manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Assets[0].URL = "http://example.test/unsafe"
	if err := protocol.ValidateReleaseManifest(manifest); err == nil {
		t.Fatal("insecure release URL was accepted")
	}
}

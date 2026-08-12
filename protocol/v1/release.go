package v1

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
)

var releaseVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)

type ReleaseManifest struct {
	Version  string         `json:"version"`
	Channel  string         `json:"channel"`
	Protocol ProtocolRange  `json:"protocol"`
	Assets   []ReleaseAsset `json:"assets"`
}

type ProtocolRange struct {
	Minimum int `json:"minimum"`
	Maximum int `json:"maximum"`
}

type ReleaseAsset struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
	MinimumOS string `json:"minimum_os"`
}

func ValidateReleaseManifest(manifest ReleaseManifest) error {
	if !releaseVersionPattern.MatchString(manifest.Version) {
		return errors.New("release version is invalid")
	}
	if manifest.Channel != "prerelease" && manifest.Channel != "stable" {
		return errors.New("release channel is invalid")
	}
	if manifest.Protocol.Minimum < 1 || manifest.Protocol.Maximum < manifest.Protocol.Minimum {
		return errors.New("release protocol range is invalid")
	}
	required := []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "windows/amd64"}
	seen := make(map[string]struct{}, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		target := asset.OS + "/" + asset.Arch
		if !slices.Contains(required, target) {
			return fmt.Errorf("release target %q is unsupported", target)
		}
		if _, exists := seen[target]; exists {
			return fmt.Errorf("release target %q is duplicated", target)
		}
		seen[target] = struct{}{}
		parsed, err := url.Parse(asset.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return fmt.Errorf("release target %q URL is invalid", target)
		}
		if len(asset.SHA256) != 64 {
			return fmt.Errorf("release target %q checksum is invalid", target)
		}
		if _, err := hex.DecodeString(asset.SHA256); err != nil {
			return fmt.Errorf("release target %q checksum is invalid", target)
		}
		signature, err := url.Parse(asset.Signature)
		if err != nil || signature.Scheme != "https" || signature.Host == "" {
			return fmt.Errorf("release target %q signature URL is invalid", target)
		}
		if asset.MinimumOS == "" {
			return fmt.Errorf("release target %q minimum OS is missing", target)
		}
	}
	if len(seen) != len(required) {
		return errors.New("release manifest does not contain every supported target")
	}
	return nil
}

// Command releasemanifest builds the signed-asset index consumed by PondPilot.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"

	protocol "github.com/pondpilot/quackridge/protocol/v1"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("releasemanifest", flag.ContinueOnError)
	version := flags.String("version", "", "release version without v prefix")
	channel := flags.String("channel", "prerelease", "prerelease or stable")
	repository := flags.String("repository", "pondpilot/quackridge", "GitHub owner/repository")
	directory := flags.String("directory", "dist", "release artifact directory")
	verify := flags.Bool("verify", false, "verify an existing manifest and local asset set")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *verify {
		return verifyManifest(*directory)
	}
	manifest := protocol.ReleaseManifest{
		Version: *version, Channel: *channel,
		Protocol: protocol.ProtocolRange{Minimum: 1, Maximum: 1},
	}
	targets := []struct{ os, arch, minimum string }{
		{"darwin", "amd64", "macOS 13"}, {"darwin", "arm64", "macOS 13"},
		{"linux", "amd64", "glibc 2.35"}, {"windows", "amd64", "Windows 10"},
	}
	baseURL := fmt.Sprintf("https://github.com/%s/releases/download/v%s/", *repository, *version)
	for _, target := range targets {
		extension := ".tar.gz"
		if target.os == "windows" {
			extension = ".zip"
		}
		name := fmt.Sprintf("quackridge_%s_%s_%s%s", *version, target.os, target.arch, extension)
		digest, err := readChecksum(filepath.Join(*directory, name+".sha256"), name)
		if err != nil {
			return err
		}
		signatureName := name + ".sigstore.json"
		if info, err := os.Stat(filepath.Join(*directory, signatureName)); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("signature bundle for %s is unavailable", name)
		}
		manifest.Assets = append(manifest.Assets, protocol.ReleaseAsset{
			OS: target.os, Arch: target.arch, URL: baseURL + name, SHA256: digest,
			Signature: baseURL + signatureName, MinimumOS: target.minimum,
		})
	}
	if err := protocol.ValidateReleaseManifest(manifest); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return os.WriteFile(filepath.Join(*directory, "release-manifest.json"), encoded, 0o644)
}

func verifyManifest(directory string) error {
	data, err := os.ReadFile(filepath.Join(directory, "release-manifest.json"))
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
	for _, asset := range manifest.Assets {
		name := pathpkg.Base(asset.URL)
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("release asset %s is unavailable", name)
		}
		digest, err := readChecksum(filepath.Join(directory, name+".sha256"), name)
		if err != nil || digest != asset.SHA256 {
			return fmt.Errorf("release asset %s checksum does not match the manifest", name)
		}
		actual, err := fileSHA256(filepath.Join(directory, name))
		if err != nil || actual != asset.SHA256 {
			return fmt.Errorf("release asset %s content does not match its checksum", name)
		}
		signatureName := pathpkg.Base(asset.Signature)
		info, err = os.Stat(filepath.Join(directory, signatureName))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("release asset %s signature bundle is unavailable", name)
		}
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func readChecksum(path, expectedName string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return "", errors.New("checksum file is empty")
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != expectedName {
		return "", errors.New("checksum file does not match its release asset")
	}
	if scanner.Scan() {
		return "", errors.New("checksum file contains multiple entries")
	}
	return fields[0], scanner.Err()
}

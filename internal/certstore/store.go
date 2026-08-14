// Package certstore manages content-addressed, public CA certificate bundles.
package certstore

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Store struct{ Root string }

const MaxBundleSize = 40 << 10

type Certificate struct {
	Reference string `json:"reference"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
}

func (s Store) Import(data []byte) (Certificate, error) {
	if len(data) == 0 || len(data) > MaxBundleSize {
		return Certificate{}, fmt.Errorf("certificate bundle must be at most %d bytes", MaxBundleSize)
	}
	canonical, err := validate(data)
	if err != nil {
		return Certificate{}, err
	}
	digest := sha256.Sum256(canonical)
	hexDigest := hex.EncodeToString(digest[:])
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		return Certificate{}, err
	}
	path := filepath.Join(s.Root, hexDigest+".pem")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return Certificate{Reference: "sha256:" + hexDigest, SHA256: hexDigest, Size: int64(len(canonical))}, nil
	}
	if err != nil {
		return Certificate{}, err
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(canonical); err != nil {
		return Certificate{}, err
	}
	if err := file.Sync(); err != nil {
		return Certificate{}, err
	}
	if err := file.Close(); err != nil {
		return Certificate{}, err
	}
	if err := syncDirectory(s.Root); err != nil {
		return Certificate{}, err
	}
	ok = true
	return Certificate{Reference: "sha256:" + hexDigest, SHA256: hexDigest, Size: int64(len(canonical))}, nil
}

func (s Store) Resolve(reference string) (string, error) {
	digest, err := parseReference(reference)
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.Root, digest+".pem")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("managed root certificate is unavailable")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	actual := sha256.Sum256(data)
	if hex.EncodeToString(actual[:]) != digest {
		return "", errors.New("managed root certificate digest mismatch")
	}
	return path, nil
}

func (s Store) List() ([]Certificate, error) {
	entries, err := os.ReadDir(s.Root)
	if errors.Is(err, os.ErrNotExist) {
		return []Certificate{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]Certificate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".pem" {
			continue
		}
		digest := strings.TrimSuffix(entry.Name(), ".pem")
		if _, err := parseReference("sha256:" + digest); err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		result = append(result, Certificate{Reference: "sha256:" + digest, SHA256: digest, Size: info.Size()})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Reference < result[j].Reference })
	return result, nil
}

func (s Store) Remove(reference string) error {
	digest, err := parseReference(reference)
	if err != nil {
		return err
	}
	path := filepath.Join(s.Root, digest+".pem")
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed certificate path is unsafe")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncDirectory(s.Root)
}

func validate(data []byte) ([]byte, error) {
	remaining := bytes.TrimSpace(data)
	var output bytes.Buffer
	count := 0
	for len(remaining) > 0 {
		block, rest := pem.Decode(remaining)
		if block == nil {
			return nil, errors.New("certificate file contains non-PEM data")
		}
		if strings.Contains(strings.ToUpper(block.Type), "PRIVATE KEY") {
			return nil, errors.New("private keys are forbidden")
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("unsupported PEM block %q", block.Type)
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, errors.New("certificate PEM is invalid")
		}
		if !certificate.BasicConstraintsValid || !certificate.IsCA || certificate.KeyUsage != 0 && certificate.KeyUsage&x509.KeyUsageCertSign == 0 {
			return nil, errors.New("certificate is not valid for CA signing")
		}
		if err := pem.Encode(&output, block); err != nil {
			return nil, err
		}
		count++
		remaining = bytes.TrimSpace(rest)
	}
	if count == 0 {
		return nil, errors.New("at least one CA certificate is required")
	}
	return output.Bytes(), nil
}

func parseReference(reference string) (string, error) {
	digest, ok := strings.CutPrefix(reference, "sha256:")
	if !ok || len(digest) != 64 {
		return "", errors.New("managed certificate reference is invalid")
	}
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("managed certificate reference is invalid")
	}
	return strings.ToLower(digest), nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

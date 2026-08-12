// Package doctor implements non-secret offline and live daemon diagnostics.
package doctor

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	quackridge "github.com/pondpilot/quackridge"
	"github.com/pondpilot/quackridge/internal/config"
	"github.com/pondpilot/quackridge/internal/control"
	"github.com/pondpilot/quackridge/internal/secrets"
)

type Level string

const (
	LevelOK      Level = "ok"
	LevelWarning Level = "warning"
	LevelError   Level = "error"
)

type Check struct {
	Name    string         `json:"name"`
	Level   Level          `json:"level"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type Report struct {
	OK     bool    `json:"ok"`
	Checks []Check `json:"checks"`
}

type Options struct {
	ConfigPath         string
	ControlAddress     string
	ExtensionDir       string
	CredentialProvider string
	CredentialStore    secrets.Store
}

func Run(ctx context.Context, options Options) Report {
	report := Report{OK: true}
	document, configOK := checkConfiguration(options.ConfigPath, &report)
	checkVersion(&report)
	checkExtensions(options.ExtensionDir, &report)
	if configOK {
		checkCredentials(ctx, document, options, &report)
	}
	checkDaemon(ctx, options.ControlAddress, &report)
	for _, check := range report.Checks {
		if check.Level == LevelError {
			report.OK = false
			break
		}
	}
	return report
}

func checkConfiguration(path string, report *Report) (config.Document, bool) {
	document, err := (config.Store{Path: path}).Load()
	if err != nil {
		add(report, "configuration", LevelError, "configuration could not be loaded", map[string]any{"error": sanitize(err)})
		return config.Document{}, false
	}
	details := map[string]any{"version": document.Version, "sources": len(document.Sources), "path": path}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		add(report, "configuration", LevelOK, "configuration is valid (no file has been created yet)", details)
		return document, true
	}
	if err != nil {
		add(report, "configuration", LevelError, "configuration permissions could not be inspected", map[string]any{"error": sanitize(err)})
		return document, false
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		details["permissions"] = fmt.Sprintf("%04o", info.Mode().Perm())
		add(report, "configuration", LevelError, "configuration must not be accessible by group or other users", details)
		return document, false
	}
	add(report, "configuration", LevelOK, "configuration is valid and access-restricted", details)
	return document, true
}

func checkVersion(report *Report) {
	add(report, "version_pair", LevelOK, "compiled protocol and DuckDB version pair is supported", map[string]any{
		"product_version": quackridge.Version, "protocol_version": quackridge.ProtocolVersion,
		"duckdb_version": quackridge.DuckDBVersion, "extensions": quackridge.ExtensionVersions(),
	})
}

type checksumEntry struct {
	name string
	hash string
}

func checkExtensions(directory string, report *Report) {
	if directory == "" {
		add(report, "extensions", LevelWarning, "extension directory was not supplied; pass --extensions to verify the installed bundle", nil)
		return
	}
	manifestPath := filepath.Join(directory, "extensions.sha256")
	entries, err := readChecksums(manifestPath)
	if err != nil {
		add(report, "extensions", LevelError, "extension checksum manifest is unavailable", map[string]any{"error": sanitize(err)})
		return
	}
	required := map[string]bool{
		"extensions.versions": false, "httpfs.duckdb_extension": false,
		"postgres_scanner.duckdb_extension": false, "quack.duckdb_extension": false,
	}
	for _, entry := range entries {
		if _, wanted := required[entry.name]; !wanted {
			add(report, "extensions", LevelError, "extension checksum manifest contains an unexpected path", map[string]any{"file": entry.name})
			return
		}
		actual, err := fileSHA256(filepath.Join(directory, entry.name))
		if err != nil || actual != entry.hash {
			add(report, "extensions", LevelError, "extension bundle checksum verification failed", map[string]any{"file": entry.name})
			return
		}
		required[entry.name] = true
	}
	for name, present := range required {
		if !present {
			add(report, "extensions", LevelError, "extension checksum manifest is incomplete", map[string]any{"file": name})
			return
		}
	}
	if err := checkExtensionVersions(filepath.Join(directory, "extensions.versions")); err != nil {
		add(report, "extensions", LevelError, "extension bundle version pair is unsupported", map[string]any{"error": sanitize(err)})
		return
	}
	add(report, "extensions", LevelOK, "all bundled extensions match their recorded checksums", map[string]any{"directory": directory})
}

func checkExtensionVersions(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	expected := quackridge.ExtensionVersions()
	expected["duckdb"] = quackridge.DuckDBVersion
	seen := make(map[string]bool, len(expected))
	scanner := bufio.NewScanner(io.LimitReader(file, 64<<10))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || expected[fields[0]] == "" || expected[fields[0]] != fields[1] || seen[fields[0]] {
			return errors.New("invalid extension version entry")
		}
		seen[fields[0]] = true
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(seen) != len(expected) {
		return errors.New("extension version manifest is incomplete")
	}
	return nil
}

func readChecksums(path string) ([]checksumEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var entries []checksumEntry
	scanner := bufio.NewScanner(io.LimitReader(file, 64<<10))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || len(fields[0]) != 64 {
			return nil, errors.New("invalid checksum entry")
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return nil, errors.New("invalid checksum digest")
		}
		name := strings.TrimPrefix(fields[1], "*")
		if filepath.Base(name) != name {
			return nil, errors.New("checksum entry must use a base filename")
		}
		entries = append(entries, checksumEntry{name: name, hash: fields[0]})
	}
	return entries, scanner.Err()
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
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func checkCredentials(ctx context.Context, document config.Document, options Options, report *Report) {
	store := options.CredentialStore
	provider := options.CredentialProvider
	if provider == "" {
		provider = "system"
	}
	if store == nil {
		var err error
		switch provider {
		case "system":
			store, err = secrets.NewSystemStore()
		case "environment":
			store = secrets.Environment{}
		default:
			err = errors.New("credential provider must be system or environment")
		}
		if err != nil {
			add(report, "credential_store", LevelError, "credential store is unavailable", map[string]any{"provider": provider, "error": sanitize(err)})
			return
		}
	}
	missing := make([]string, 0)
	for _, source := range document.Sources {
		if !source.Enabled {
			continue
		}
		credential, err := store.Get(ctx, source.CredentialRef)
		if err != nil || len(credential) == 0 {
			missing = append(missing, source.ID)
		}
		clear(credential)
	}
	if len(missing) > 0 {
		add(report, "credential_store", LevelError, "credentials are unavailable for enabled sources", map[string]any{"provider": provider, "source_ids": missing})
		return
	}
	add(report, "credential_store", LevelOK, "credential store is accessible for every enabled source", map[string]any{"provider": provider})
}

func checkDaemon(ctx context.Context, address string, report *Report) {
	if address == "" {
		add(report, "daemon", LevelWarning, "control address is unavailable; live checks were skipped", nil)
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	statusResponse, err := control.Call(callCtx, address, control.Request{Version: control.Version, Operation: "status"})
	if err != nil {
		if info, statErr := os.Lstat(address); statErr == nil && info.Mode()&os.ModeSocket != 0 {
			add(report, "daemon", LevelWarning, "a stale control socket may be present", map[string]any{"address": address})
		} else {
			add(report, "daemon", LevelWarning, "QuackRidge is not running; live source and limit checks were skipped", map[string]any{"address": address})
		}
		return
	}
	if !statusResponse.OK || statusResponse.Status == nil {
		add(report, "daemon", LevelError, "daemon returned an invalid status response", nil)
		return
	}
	if !loopbackEndpoint(statusResponse.Status.Endpoint) {
		add(report, "loopback", LevelError, "data-plane endpoint is not loopback-only", nil)
	} else {
		add(report, "loopback", LevelOK, "data-plane endpoint is loopback-only", map[string]any{"endpoint": statusResponse.Status.Endpoint})
	}
	diagnosticsResponse, err := control.Call(callCtx, address, control.Request{Version: control.Version, Operation: "diagnostics"})
	if err != nil || !diagnosticsResponse.OK {
		add(report, "daemon", LevelError, "live diagnostics are unavailable", nil)
		return
	}
	addSourceDiagnosticChecks(report, diagnosticsResponse.Diagnostics["source_diagnostics"])
	add(report, "daemon", LevelOK, "live daemon, source health, and locked resource limits are available", diagnosticsResponse.Diagnostics)
}

func addSourceDiagnosticChecks(report *Report, value any) {
	entries, ok := value.([]any)
	if !ok {
		add(report, "source_diagnostics", LevelError, "live source diagnostics are malformed", nil)
		return
	}
	for _, entry := range entries {
		diagnostic, ok := entry.(map[string]any)
		if !ok {
			add(report, "source_diagnostics", LevelError, "live source diagnostics are malformed", nil)
			continue
		}
		id, _ := diagnostic["id"].(string)
		health, _ := diagnostic["health"].(string)
		if id == "" || health == "" {
			add(report, "source_diagnostics", LevelError, "live source diagnostics are malformed", nil)
			continue
		}
		if health != "ready" {
			add(report, "source_health", LevelError, "source connectivity check failed", map[string]any{"source_id": id})
		}
		warnings, ok := diagnostic["warnings"].([]any)
		if !ok {
			add(report, "source_diagnostics", LevelError, "live source diagnostics are malformed", map[string]any{"source_id": id})
			continue
		}
		for _, warning := range warnings {
			message, ok := warning.(string)
			if !ok || message == "" {
				add(report, "source_diagnostics", LevelError, "live source diagnostics are malformed", map[string]any{"source_id": id})
				continue
			}
			add(report, "source_posture", LevelWarning, message, map[string]any{"source_id": id})
		}
	}
}

func loopbackEndpoint(endpoint string) bool {
	address := strings.TrimPrefix(endpoint, "quack:")
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func add(report *Report, name string, level Level, message string, details map[string]any) {
	report.Checks = append(report.Checks, Check{Name: name, Level: level, Message: message, Details: details})
}

func sanitize(err error) string {
	if err == nil {
		return ""
	}
	var syntaxError *json.SyntaxError
	if errors.As(err, &syntaxError) {
		return "invalid JSON"
	}
	return err.Error()
}

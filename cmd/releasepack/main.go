// Command releasepack creates deterministic, platform-native QuackRidge archives.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type entry struct {
	name string
	path string
	mode fs.FileMode
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("releasepack", flag.ContinueOnError)
	version := flags.String("version", "", "release version without v prefix")
	osName := flags.String("os", "", "target operating system")
	arch := flags.String("arch", "", "target architecture")
	binary := flags.String("binary", "", "built QuackRidge binary")
	extensions := flags.String("extensions", "", "verified extension directory")
	sbom := flags.String("sbom", "", "SPDX JSON SBOM")
	output := flags.String("output", "dist", "output directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *version == "" || *binary == "" || *extensions == "" || *sbom == "" {
		return errors.New("version, binary, extensions, and sbom are required")
	}
	if !validTarget(*osName, *arch) {
		return fmt.Errorf("unsupported release target %s/%s", *osName, *arch)
	}
	timestamp := time.Unix(0, 0).UTC()
	if raw := os.Getenv("SOURCE_DATE_EPOCH"); raw != "" {
		seconds, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || seconds < 0 {
			return errors.New("SOURCE_DATE_EPOCH must be a non-negative Unix timestamp")
		}
		timestamp = time.Unix(seconds, 0).UTC()
	}
	binaryName := "quackridge"
	if *osName == "windows" {
		binaryName += ".exe"
	}
	root := fmt.Sprintf("quackridge_%s_%s_%s", *version, *osName, *arch)
	files := []entry{
		{name: root + "/" + binaryName, path: *binary, mode: 0o755},
		{name: root + "/LICENSE", path: "LICENSE", mode: 0o644},
		{name: root + "/README.md", path: "README.md", mode: 0o644},
		{name: root + "/SECURITY.md", path: "SECURITY.md", mode: 0o644},
		{name: root + "/THIRD_PARTY_NOTICES.md", path: "THIRD_PARTY_NOTICES.md", mode: 0o644},
		{name: root + "/licenses/DuckDB-LICENSE", path: "third_party/duckdb/LICENSE", mode: 0o644},
		{name: root + "/licenses/Quack-LICENSE", path: "third_party/quack/LICENSE", mode: 0o644},
		{name: root + "/sbom.spdx.json", path: *sbom, mode: 0o644},
	}
	for _, name := range []string{
		"extensions.sha256", "extensions.upstream", "extensions.versions", "httpfs.duckdb_extension",
		"postgres_scanner.duckdb_extension", "quack.duckdb_extension",
	} {
		files = append(files, entry{name: root + "/extensions/" + name, path: filepath.Join(*extensions, name), mode: 0o644})
	}
	for _, file := range files {
		info, err := os.Stat(file.path)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("required release input %q is unavailable", file.path)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	if err := os.MkdirAll(*output, 0o755); err != nil {
		return err
	}
	extension := ".tar.gz"
	if *osName == "windows" {
		extension = ".zip"
	}
	archivePath := filepath.Join(*output, root+extension)
	if *osName == "windows" {
		if err := writeZIP(archivePath, files, timestamp); err != nil {
			return err
		}
	} else if err := writeTarGZ(archivePath, files, timestamp); err != nil {
		return err
	}
	digest, err := fileDigest(archivePath)
	if err != nil {
		return err
	}
	checksumPath := archivePath + ".sha256"
	if err := os.WriteFile(checksumPath, []byte(digest+"  "+filepath.Base(archivePath)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Println(archivePath)
	return nil
}

func validTarget(osName, arch string) bool {
	return osName == "darwin" && (arch == "amd64" || arch == "arm64") ||
		osName == "linux" && arch == "amd64" || osName == "windows" && arch == "amd64"
}

func writeTarGZ(path string, files []entry, timestamp time.Time) (returnErr error) {
	output, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, output.Close()) }()
	gzipWriter, err := gzip.NewWriterLevel(output, gzip.BestCompression)
	if err != nil {
		return err
	}
	gzipWriter.Header.ModTime = timestamp
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	for _, file := range files {
		data, err := os.ReadFile(file.path)
		if err != nil {
			return err
		}
		header := &tar.Header{Name: file.name, Mode: int64(file.mode), Size: int64(len(data)), ModTime: timestamp, Format: tar.FormatPAX}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if _, err := tarWriter.Write(data); err != nil {
			return err
		}
	}
	return errors.Join(tarWriter.Close(), gzipWriter.Close())
}

func writeZIP(path string, files []entry, timestamp time.Time) (returnErr error) {
	output, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, output.Close()) }()
	writer := zip.NewWriter(output)
	defer func() { returnErr = errors.Join(returnErr, writer.Close()) }()
	for _, file := range files {
		data, err := os.ReadFile(file.path)
		if err != nil {
			return err
		}
		header := &zip.FileHeader{Name: strings.ReplaceAll(file.name, "\\", "/"), Method: zip.Deflate}
		header.SetMode(file.mode)
		header.SetModTime(timestamp)
		entryWriter, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		if _, err := entryWriter.Write(data); err != nil {
			return err
		}
	}
	return nil
}

func fileDigest(path string) (string, error) {
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

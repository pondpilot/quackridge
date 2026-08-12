package main

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestWriteTarGZIsDeterministicAndSorted(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "first")
	second := filepath.Join(directory, "second")
	if err := os.WriteFile(first, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := []entry{{name: "root/a", path: first, mode: 0o644}, {name: "root/b", path: second, mode: 0o644}}
	epoch := time.Unix(0, 0).UTC()
	one := filepath.Join(directory, "one.tar.gz")
	two := filepath.Join(directory, "two.tar.gz")
	if err := writeTarGZ(one, files, epoch); err != nil {
		t.Fatal(err)
	}
	if err := writeTarGZ(two, files, epoch); err != nil {
		t.Fatal(err)
	}
	oneDigest, _ := fileDigest(one)
	twoDigest, _ := fileDigest(two)
	if oneDigest != twoDigest {
		t.Fatalf("archives differ: %s != %s", oneDigest, twoDigest)
	}
	file, err := os.Open(one)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(gz)
	var names []string
	for {
		header, err := reader.Next()
		if err != nil {
			break
		}
		names = append(names, header.Name)
	}
	if !reflect.DeepEqual(names, []string{"root/a", "root/b"}) {
		t.Fatalf("entries = %#v", names)
	}
}

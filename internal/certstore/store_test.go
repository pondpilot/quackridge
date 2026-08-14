package certstore

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"testing"
	"time"
)

func testCertificate(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "QuackRidge Test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestImportResolveListAndRemove(t *testing.T) {
	store := Store{Root: t.TempDir()}
	imported, err := store.Import(testCertificate(t))
	if err != nil {
		t.Fatal(err)
	}
	path, err := store.Resolve(imported.Reference)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions = %v, %v", info, err)
	}
	listed, err := store.List()
	if err != nil || len(listed) != 1 || listed[0].Reference != imported.Reference {
		t.Fatalf("list = %#v, %v", listed, err)
	}
	if err := store.Remove(imported.Reference); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve(imported.Reference); err == nil {
		t.Fatal("removed certificate resolved")
	}
}

func TestRejectsPrivateKeyInvalidAndTraversal(t *testing.T) {
	store := Store{Root: t.TempDir()}
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	private := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	for _, value := range [][]byte{private, []byte("not pem")} {
		if _, err := store.Import(value); err == nil {
			t.Fatal("unsafe PEM imported")
		}
	}
	if _, err := store.Resolve("sha256:../../escape"); err == nil {
		t.Fatal("traversal resolved")
	}
}

func TestRejectsBundleLargerThanManagementFrameBudget(t *testing.T) {
	store := Store{Root: t.TempDir()}
	if _, err := store.Import(make([]byte, MaxBundleSize+1)); err == nil {
		t.Fatal("oversized certificate bundle was accepted")
	}
}

func TestRejectsLeafCertificate(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Leaf"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if _, err := (Store{Root: t.TempDir()}).Import(leaf); err == nil {
		t.Fatal("leaf certificate imported as a CA")
	}
}

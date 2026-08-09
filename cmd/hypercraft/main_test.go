package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadTLSWithoutFlagsServesPlainHTTP(t *testing.T) {
	cfg, err := loadTLS("", "")
	if err != nil {
		t.Fatalf("loadTLS: %v", err)
	}
	if cfg != nil {
		t.Error("no certificate flags should mean no TLS config")
	}
}

// Half a pair is a typo, not a configuration. Saying so is better than
// silently falling back to plain HTTP on a panel the operator believes is
// encrypted.
func TestLoadTLSRejectsHalfAPair(t *testing.T) {
	certPath, keyPath := writeSelfSigned(t)

	if _, err := loadTLS(certPath, ""); err == nil {
		t.Error("a certificate without a key should be rejected")
	}
	if _, err := loadTLS("", keyPath); err == nil {
		t.Error("a key without a certificate should be rejected")
	}
}

// The failure has to arrive before the panel claims the port and starts the
// Minecraft servers, and it has to name the file.
func TestLoadTLSReportsAMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := loadTLS(filepath.Join(dir, "nope.pem"), filepath.Join(dir, "nope.key"))
	if err == nil {
		t.Fatal("a missing certificate should be an error")
	}
	if !strings.Contains(err.Error(), "nope.pem") {
		t.Errorf("error should name the file it could not read, got: %v", err)
	}
}

func TestLoadTLSLoadsAValidPair(t *testing.T) {
	certPath, keyPath := writeSelfSigned(t)

	cfg, err := loadTLS(certPath, keyPath)
	if err != nil {
		t.Fatalf("loadTLS: %v", err)
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("expected one certificate, got %d", len(cfg.Certificates))
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %#x, want TLS 1.2 (%#x)", cfg.MinVersion, tls.VersionTLS12)
	}
}

// writeSelfSigned produces a throwaway certificate and key on disk, which is
// all loadTLS needs to be exercised for real rather than against a fixture.
func writeSelfSigned(t *testing.T) (certPath, keyPath string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "hypercraft-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	dir := t.TempDir()
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	write := func(path string, block *pem.Block) {
		if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(certPath, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	write(keyPath, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPath, keyPath
}

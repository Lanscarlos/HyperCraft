package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lanscarlos/hypercraft/internal/auth"
	"github.com/lanscarlos/hypercraft/internal/config"
	"github.com/lanscarlos/hypercraft/internal/store"
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

// The migration from a single operator to accounts. This is the one path every
// existing panel takes exactly once, and getting it wrong locks somebody out of
// their own machine, so it is checked end to end rather than in pieces.
func TestOpenAccountsMigratesTheSingleOperator(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	st, err := store.New(paths)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	const password = "the-old-password"
	cred, err := auth.NewCredential("carlos", password)
	if err != nil {
		t.Fatalf("NewCredential: %v", err)
	}
	panel := config.Defaults()
	panel.Credential = cred
	// A device paired before accounts existed: it was minted by this very
	// password, so it has to end up belonging to the account that inherits it.
	panel.Devices = []auth.DeviceToken{{ID: "dev1", Name: "phone", Hash: "deadbeef"}}
	if err := st.SavePanel(panel); err != nil {
		t.Fatalf("SavePanel: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	accounts, err := openAccounts(st, &panel, "admin", logger)
	if err != nil {
		t.Fatalf("openAccounts: %v", err)
	}

	// The operator is now an administrator, and their password still works.
	admin, ok := accounts.FirstAdmin()
	if !ok {
		t.Fatal("no administrator after the migration")
	}
	if admin.Username != "carlos" {
		t.Errorf("the administrator is %q, want carlos — the -username default overrode the stored name", admin.Username)
	}
	if _, err := accounts.Authenticate("carlos", password); err != nil {
		t.Errorf("the operator's password stopped working: %v", err)
	}
	if panel.Devices[0].UserID != admin.ID {
		t.Errorf("the existing pairing went to %q, want the administrator", panel.Devices[0].UserID)
	}
	// users.json is authoritative now, so the field it replaced is cleared.
	if !panel.Credential.IsZero() {
		t.Error("the legacy credential is still in the panel config")
	}

	// Running again must not migrate a second time or disturb what is there.
	if err := st.SavePanel(panel); err != nil {
		t.Fatalf("SavePanel: %v", err)
	}
	again, err := openAccounts(st, &panel, "admin", logger)
	if err != nil {
		t.Fatalf("openAccounts (second run): %v", err)
	}
	if got := again.List(); len(got) != 1 || got[0].ID != admin.ID {
		t.Errorf("a second start changed the accounts: %+v", got)
	}
}

// A brand-new panel takes the same path, minting the credential rather than
// adopting one.
func TestOpenAccountsBootstrapsAFreshPanel(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	st, err := store.New(paths)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	panel := config.Defaults()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	accounts, err := openAccounts(st, &panel, "lans", logger)
	if err != nil {
		t.Fatalf("openAccounts: %v", err)
	}

	admin, ok := accounts.FirstAdmin()
	if !ok {
		t.Fatal("a fresh panel has no administrator")
	}
	if admin.Username != "lans" {
		t.Errorf("the administrator is %q, want the -username value", admin.Username)
	}
	if _, _, err := st.LoadUsers(); err != nil {
		t.Errorf("users.json was not written: %v", err)
	}
}

// users.json holds every password hash on the panel, so it must not be
// readable by anyone else on the machine.
func TestUsersFileIsNotWorldReadable(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	st, err := store.New(paths)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	panel := config.Defaults()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := openAccounts(st, &panel, "admin", logger); err != nil {
		t.Fatalf("openAccounts: %v", err)
	}

	info, err := os.Stat(paths.UsersFile())
	if err != nil {
		t.Fatalf("stat users.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("users.json is %o, want 600", perm)
	}
}

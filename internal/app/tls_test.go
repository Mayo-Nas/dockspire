package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPersistentCertificateAndHTTPSOnly(t *testing.T) {
	root := t.TempDir()
	certificate, err := serverCertificate(root, "", "")
	if err != nil {
		t.Fatal(err)
	}
	reused, err := serverCertificate(root, "", "")
	if err != nil || !bytes.Equal(certificate.Certificate[0], reused.Certificate[0]) {
		t.Fatal("certificate changed on restart", err)
	}
	custom, err := serverCertificate("unused", filepath.Join(root, "tls", "cert.pem"), filepath.Join(root, "tls", "key.pem"))
	if err != nil || !bytes.Equal(custom.Certificate[0], reused.Certificate[0]) {
		t.Fatal("custom certificate failed", err)
	}
	_, handler, _ := setupFixture(t)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	server.StartTLS()
	defer server.Close()
	if err := checkLocalHealth(server.Listener.Addr().String(), true); err != nil {
		t.Fatal("TLS health probe failed", err)
	}
	response, err := server.Client().Get(server.URL + "/setup")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.TLS == nil || response.TLS.Version < tls.VersionTLS12 || response.StatusCode != http.StatusOK {
		t.Fatal("HTTPS setup failed")
	}
	if err := checkLocalHealth(server.Listener.Addr().String(), false); err == nil {
		t.Fatal("TLS server accepted plaintext health")
	}
	if err := os.WriteFile(filepath.Join(root, "tls", "cert.pem"), []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := serverCertificate(root, "", ""); err == nil {
		t.Fatal("damaged certificate silently replaced")
	}
}

func TestTLSFingerprintCLIUsesOnlyPublicCertificate(t *testing.T) {
	root := t.TempDir()
	certificate, err := serverCertificate(root, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "tls", "key.pem")); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "-test.run=^TestAdminCLI$", "--", "--tls-fingerprint", "--data-dir", root)
	command.Env = append(os.Environ(), "STACKLANE_TEST_ADMIN_CLI=1")
	output, err := command.CombinedOutput()
	if err != nil || strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0]) != "SHA256 Fingerprint="+certificateFingerprint(certificate.Certificate[0]) {
		t.Fatal("offline public certificate command failed", err)
	}
	if _, err := os.Stat(filepath.Join(root, "admin.json")); !os.IsNotExist(err) {
		t.Fatal("fingerprint command initialized administrator")
	}
}

func TestTLSFingerprintReadsOnlyExistingPublicCertificate(t *testing.T) {
	root := t.TempDir()
	certificate, err := serverCertificate(root, "", "", "vm.example.test", "203.0.113.20")
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(certificate.Certificate[0])
	if leaf.VerifyHostname("vm.example.test") != nil || leaf.VerifyHostname("203.0.113.20") != nil {
		t.Fatal("requested TLS identities missing")
	}
	before, _ := os.ReadFile(filepath.Join(root, "tls", "cert.pem"))
	if err := os.Remove(filepath.Join(root, "tls", "key.pem")); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := readCertificateFingerprint(root, "")
	if err != nil || fingerprint != certificateFingerprint(certificate.Certificate[0]) || len(strings.Split(fingerprint, ":")) != 32 {
		t.Fatal("public fingerprint failed", err)
	}
	if _, err := readCertificateFingerprint(filepath.Join(root, "missing"), ""); err == nil {
		t.Fatal("fingerprint command created a missing certificate")
	}
	after, _ := os.ReadFile(filepath.Join(root, "tls", "cert.pem"))
	if !bytes.Equal(before, after) {
		t.Fatal("fingerprint command changed server identity")
	}
}

func TestTLSHostsDoNotSilentlyChangeSavedIdentity(t *testing.T) {
	root := t.TempDir()
	if _, err := serverCertificate(root, "", "", "vm.example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := serverCertificate(root, "", "", "different.example.test"); err == nil {
		t.Fatal("existing identity silently replaced")
	}
	if _, err := serverCertificate(t.TempDir(), "", "", "https://invalid.example.test:9443"); err == nil {
		t.Fatal("URL accepted as certificate SAN")
	}
}

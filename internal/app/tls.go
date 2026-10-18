package app

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var tlsDNSName = regexp.MustCompile(`(?i)^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.?$`)

func serverCertificate(root, certFile, keyFile string, hosts ...string) (tls.Certificate, error) {
	for _, host := range hosts {
		if host != "" && net.ParseIP(host) == nil && (len(host) > 253 || !tlsDNSName.MatchString(host)) {
			return tls.Certificate{}, errors.New("TLS hosts must be DNS names or IP addresses without ports or URL schemes")
		}
	}
	if certFile != "" && keyFile != "" {
		return tls.LoadX509KeyPair(certFile, keyFile)
	}
	directory := filepath.Join(root, "tls")
	if err := ensurePrivateDirectory(root); err != nil {
		return tls.Certificate{}, err
	}
	info, err := os.Lstat(directory)
	if os.IsNotExist(err) {
		// Publish the pair together so interruption cannot leave half a certificate.
		stage, err := os.MkdirTemp(root, ".tls-")
		if err != nil {
			return tls.Certificate{}, err
		}
		defer os.RemoveAll(stage)
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return tls.Certificate{}, err
		}
		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if err != nil {
			return tls.Certificate{}, err
		}
		now := time.Now()
		template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Dockspire"},
			NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(2, 0, 0),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}}
		for _, host := range hosts {
			if host == "" {
				continue
			}
			if ip := net.ParseIP(host); ip != nil {
				template.IPAddresses = append(template.IPAddresses, ip)
			} else {
				template.DNSNames = append(template.DNSNames, host)
			}
		}
		der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if err != nil {
			return tls.Certificate{}, err
		}
		private, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return tls.Certificate{}, err
		}
		if err := writePrivateFile(filepath.Join(stage, "cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
			return tls.Certificate{}, err
		}
		if err := writePrivateFile(filepath.Join(stage, "key.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})); err != nil {
			return tls.Certificate{}, err
		}
		if err := syncDirectory(stage); err != nil {
			return tls.Certificate{}, err
		}
		if err := os.Rename(stage, directory); err != nil {
			return tls.Certificate{}, err
		}
		if err := syncDirectory(root); err != nil {
			return tls.Certificate{}, err
		}
	}
	info, err = os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return tls.Certificate{}, errors.New("Unsafe TLS directory")
	}
	for _, name := range []string{"cert.pem", "key.pem"} {
		info, err := os.Lstat(filepath.Join(directory, name))
		if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
			return tls.Certificate{}, errors.New("TLS files are missing or unsafe; refusing to replace existing files")
		}
	}
	certificate, err := tls.LoadX509KeyPair(filepath.Join(directory, "cert.pem"), filepath.Join(directory, "key.pem"))
	if err != nil {
		return tls.Certificate{}, err
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return tls.Certificate{}, err
	}
	for _, host := range hosts {
		if host != "" && leaf.VerifyHostname(host) != nil {
			return tls.Certificate{}, errors.New("Saved TLS certificate does not cover the requested host; supply a matching custom certificate instead of silently changing server identity")
		}
	}
	return certificate, nil
}

func certificateFingerprint(der []byte) string {
	digest := sha256.Sum256(der)
	parts := make([]string, len(digest))
	for i, value := range digest {
		parts[i] = fmt.Sprintf("%02X", value)
	}
	return strings.Join(parts, ":")
}

// Read only the public certificate. This command never generates a certificate,
// opens the private key/admin file or connects to the network.
func readCertificateFingerprint(root, certFile string) (string, error) {
	if certFile == "" {
		certFile = filepath.Join(root, "tls", "cert.pem")
	}
	info, err := os.Lstat(certFile)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return "", errors.New("Public TLS certificate missing or unsafe")
	}
	data, err := os.ReadFile(certFile)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("Invalid public TLS certificate")
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return "", err
	}
	return certificateFingerprint(block.Bytes), nil
}

func checkLocalHealth(listen string, https bool) error {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return err
	}
	scheme := "http"
	if https {
		scheme = "https"
	}
	// This probe never leaves loopback and sends no cookie, password or user data.
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 4 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(scheme + "://127.0.0.1:" + port + "/api/health")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Health check returned %d", response.StatusCode)
	}
	return nil
}

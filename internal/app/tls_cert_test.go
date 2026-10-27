package app

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureSelfSignedCertGeneratesAndCaches(t *testing.T) {
	tempDir := t.TempDir()

	certFile, keyFile, err := EnsureSelfSignedCert(tempDir)
	if err != nil {
		t.Fatalf("EnsureSelfSignedCert failed: %v", err)
	}

	certBytes, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatalf("read cert file: %v", err)
	}
	keyBytes, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}

	block, _ := pem.Decode(certBytes)
	if block == nil {
		t.Fatal("expected certificate PEM block, got nil")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}

	if cert.Subject.CommonName != "Zhulong Edge Self-Signed" {
		t.Errorf("expected CommonName 'Zhulong Edge Self-Signed', got %q", cert.Subject.CommonName)
	}

	// Verify SANs contain localhost and 127.0.0.1
	foundLocalhost := false
	for _, dns := range cert.DNSNames {
		if dns == "localhost" {
			foundLocalhost = true
			break
		}
	}
	if !foundLocalhost {
		t.Errorf("expected DNS SAN 'localhost' in %+v", cert.DNSNames)
	}

	foundLoopbackIP := false
	for _, ip := range cert.IPAddresses {
		if ip.String() == "127.0.0.1" {
			foundLoopbackIP = true
			break
		}
	}
	if !foundLoopbackIP {
		t.Errorf("expected IP SAN 127.0.0.1 in %+v", cert.IPAddresses)
	}

	// Ensure TLS keypair loads successfully
	if _, err := tls.X509KeyPair(certBytes, keyBytes); err != nil {
		t.Fatalf("tls.X509KeyPair failed on generated cert: %v", err)
	}

	// Calling EnsureSelfSignedCert again should reuse the existing files
	statBefore, err := os.Stat(certFile)
	if err != nil {
		t.Fatal(err)
	}

	certFile2, keyFile2, err := EnsureSelfSignedCert(tempDir)
	if err != nil {
		t.Fatalf("second EnsureSelfSignedCert failed: %v", err)
	}
	if certFile != certFile2 || keyFile != keyFile2 {
		t.Errorf("expected reused paths, got %s and %s", certFile2, keyFile2)
	}

	statAfter, err := os.Stat(certFile)
	if err != nil {
		t.Fatal(err)
	}
	if statBefore.ModTime() != statAfter.ModTime() {
		t.Errorf("expected certificate file to be cached and unchanged, but modtime changed")
	}
}

func TestBuildTLSConfigModes(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Off / empty mode
	tlsCfg, err := BuildTLSConfig(Config{TLSMode: "off"})
	if err != nil || tlsCfg != nil {
		t.Fatalf("expected nil tlsConfig for mode 'off', got %v, err: %v", tlsCfg, err)
	}
	tlsCfg, err = BuildTLSConfig(Config{TLSMode: ""})
	if err != nil || tlsCfg != nil {
		t.Fatalf("expected nil tlsConfig for empty mode, got %v, err: %v", tlsCfg, err)
	}

	// 2. Auto mode
	tlsCfg, err = BuildTLSConfig(Config{
		TLSMode: "auto",
		DataDir: tempDir,
	})
	if err != nil {
		t.Fatalf("BuildTLSConfig with auto failed: %v", err)
	}
	if tlsCfg == nil || len(tlsCfg.Certificates) == 0 {
		t.Fatal("expected non-empty certificates in auto TLS config")
	}

	// 3. Manual mode with valid files
	certPath := filepath.Join(tempDir, "certs", selfSignedCertFileName)
	keyPath := filepath.Join(tempDir, "certs", selfSignedKeyFileName)
	tlsCfgManual, err := BuildTLSConfig(Config{
		TLSMode:     "manual",
		TLSCertFile: certPath,
		TLSKeyFile:  keyPath,
	})
	if err != nil {
		t.Fatalf("BuildTLSConfig with manual failed: %v", err)
	}
	if tlsCfgManual == nil || len(tlsCfgManual.Certificates) == 0 {
		t.Fatal("expected non-empty certificates in manual TLS config")
	}

	// 4. Manual mode with missing files
	_, err = BuildTLSConfig(Config{
		TLSMode:     "manual",
		TLSCertFile: "",
		TLSKeyFile:  "",
	})
	if err == nil {
		t.Fatal("expected error for manual mode with empty cert/key files")
	}

	// 5. Invalid mode
	_, err = BuildTLSConfig(Config{
		TLSMode: "unknown_mode",
	})
	if err == nil {
		t.Fatal("expected error for unknown tls_mode")
	}
}

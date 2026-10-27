package app

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	selfSignedCertFileName = "zhulong_self_signed.crt"
	selfSignedKeyFileName  = "zhulong_self_signed.key"
)

// EnsureSelfSignedCert ensures that a self-signed X.509 certificate and RSA private key
// exist under dataDir/certs and are valid for at least another 30 days.
// If valid files exist, their paths are returned immediately. Otherwise, a new certificate
// with SANs including localhost, loopback IPs, and all active host network interfaces is created.
func EnsureSelfSignedCert(dataDir string) (certFile, keyFile string, err error) {
	if stringsTrim := filepath.Clean(dataDir); stringsTrim == "" || stringsTrim == "." && dataDir == "" {
		dataDir = "./data"
	}
	certsDir := filepath.Join(dataDir, "certs")
	certPath := filepath.Join(certsDir, selfSignedCertFileName)
	keyPath := filepath.Join(certsDir, selfSignedKeyFileName)

	if isValidCertAndKey(certPath, keyPath) {
		return certPath, keyPath, nil
	}

	if err := os.MkdirAll(certsDir, 0o700); err != nil {
		return "", "", fmt.Errorf("create certs directory %q: %w", certsDir, err)
	}

	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", fmt.Errorf("generate RSA private key: %w", err)
	}

	ipAddrs := collectHostIPAddresses()
	dnsNames := []string{"localhost"}
	if host, err := os.Hostname(); err == nil && host != "" && host != "localhost" {
		dnsNames = append(dnsNames, host)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return "", "", fmt.Errorf("generate certificate serial number: %w", err)
	}

	now := time.Now()
	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Zhulong Edge Security"},
			CommonName:   "Zhulong Edge Self-Signed",
		},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           ipAddrs,
		DNSNames:              dnsNames,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privKey.PublicKey, privKey)
	if err != nil {
		return "", "", fmt.Errorf("create self-signed certificate: %w", err)
	}

	keyBytes := x509.MarshalPKCS1PrivateKey(privKey)
	keyPem := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyBytes})
	certPem := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})

	tmpKeyPath := keyPath + ".tmp"
	if err := os.WriteFile(tmpKeyPath, keyPem, 0o600); err != nil {
		return "", "", fmt.Errorf("write private key: %w", err)
	}
	if err := os.Rename(tmpKeyPath, keyPath); err != nil {
		_ = os.Remove(tmpKeyPath)
		return "", "", fmt.Errorf("commit private key file: %w", err)
	}

	tmpCertPath := certPath + ".tmp"
	if err := os.WriteFile(tmpCertPath, certPem, 0o644); err != nil {
		return "", "", fmt.Errorf("write certificate: %w", err)
	}
	if err := os.Rename(tmpCertPath, certPath); err != nil {
		_ = os.Remove(tmpCertPath)
		return "", "", fmt.Errorf("commit certificate file: %w", err)
	}

	return certPath, keyPath, nil
}

func isValidCertAndKey(certPath, keyPath string) bool {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return false
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return false
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return false
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return false
	}

	// Must have at least 30 days left
	if time.Until(cert.NotAfter) < 30*24*time.Hour {
		return false
	}

	_, err = tls.X509KeyPair(certPEM, keyPEM)
	return err == nil
}

func collectHostIPAddresses() []net.IP {
	seen := make(map[string]bool)
	var ips []net.IP

	addIP := func(ip net.IP) {
		if ip == nil {
			return
		}
		str := ip.String()
		if !seen[str] {
			seen[str] = true
			ips = append(ips, ip)
		}
	}

	addIP(net.ParseIP("127.0.0.1"))
	addIP(net.ParseIP("::1"))

	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && !ip.IsLoopback() {
				addIP(ip)
			}
		}
	}

	return ips
}

// BuildTLSConfig constructs a *tls.Config based on the application's Config.
// Returns (nil, nil) if TLSMode is "off" or empty.
func BuildTLSConfig(config Config) (*tls.Config, error) {
	switch config.TLSMode {
	case "", "off":
		return nil, nil
	case "manual":
		if config.TLSCertFile == "" || config.TLSKeyFile == "" {
			return nil, errors.New("manual TLS mode requires cert_file and key_file")
		}
		cert, err := tls.LoadX509KeyPair(config.TLSCertFile, config.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load manual TLS key pair: %w", err)
		}
		return &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}, nil
	case "auto":
		certPath, keyPath, err := EnsureSelfSignedCert(config.DataDir)
		if err != nil {
			return nil, fmt.Errorf("ensure self-signed certificate: %w", err)
		}
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("load self-signed TLS key pair: %w", err)
		}
		return &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported tls_mode %q: must be off, auto, or manual", config.TLSMode)
	}
}

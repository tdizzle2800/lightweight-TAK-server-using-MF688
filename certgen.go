package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"
)

// ensureCert creates a self-signed certificate and key at certPath/keyPath when
// neither file exists yet. It never overwrites: if both exist it does nothing,
// and if only one exists it returns an error so a mismatched pair is not used.
// The certificate is valid for 10 years and names the bind address, so the
// puck can make its own TLS identity without OpenSSL on the computer.
func ensureCert(certPath, keyPath, bindAddr string) (created bool, err error) {
	_, cErr := os.Stat(certPath)
	_, kErr := os.Stat(keyPath)
	switch {
	case cErr == nil && kErr == nil:
		return false, nil
	case cErr == nil || kErr == nil:
		return false, fmt.Errorf("only one of %s and %s exists; remove it or supply both", certPath, keyPath)
	case !errors.Is(cErr, os.ErrNotExist) || !errors.Is(kErr, os.ErrNotExist):
		return false, fmt.Errorf("cannot check cert files: %v / %v", cErr, kErr)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return false, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 63))
	if err != nil {
		return false, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "puck-tak", Organization: []string{"puck-tak"}},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"puck-tak"},
	}
	if ip := net.ParseIP(bindAddr); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
		tmpl.Subject.CommonName = bindAddr
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return false, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return false, err
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		os.Remove(keyPath)
		return false, err
	}
	return true, nil
}

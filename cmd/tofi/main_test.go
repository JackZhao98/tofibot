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
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerTLSConfigurationFailsClosed(t *testing.T) {
	config, err := serverTLSConfig("", "")
	if err != nil || config != nil {
		t.Fatal("legacy HTTP configuration changed")
	}
	for _, pair := range [][2]string{{"certificate", ""}, {"", "key"}, {"missing-cert", "missing-key"}} {
		if config, err = serverTLSConfig(pair[0], pair[1]); err == nil || config != nil {
			t.Fatal("invalid TLS configuration fell back to HTTP")
		}
	}
}

func TestServerTLSLoadsOperatorPair(t *testing.T) {
	// Synthetic certificate exists only inside this test's temporary directory.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic.invalid"}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	cert, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyBytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes}), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := serverTLSConfig(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if config.MinVersion < tls.VersionTLS12 || len(config.Certificates) != 1 {
		t.Fatal("TLS configuration incomplete")
	}
}

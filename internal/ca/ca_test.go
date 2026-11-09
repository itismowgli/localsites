package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"testing"
	"time"
)

func TestIssueAllowedNames(t *testing.T) {
	c, err := Load(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	tc, err := c.Issue("shop.test", "localhost", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tc.Leaf.Verify(x509.VerifyOptions{DNSName: "shop.test", Roots: c.Pool()}); err != nil {
		t.Fatalf("valid leaf did not verify: %v", err)
	}
	for _, bad := range []string{"example.com", "google.com", "test.evil.com", "10.0.0.1"} {
		if _, err := c.Issue(bad); err == nil {
			t.Errorf("issued a certificate for %s", bad)
		}
	}
}

// Even a certificate forged directly with the CA key must fail verification for real domains.
func TestNameConstraintsBlockForgedCerts(t *testing.T) {
	c, err := Load(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	lk, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: randSerial(), Subject: pkix.Name{CommonName: "bank.com"},
		DNSNames: []string{"bank.com"}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &lk.PublicKey, c.key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: "bank.com", Roots: c.Pool()}); err == nil {
		t.Fatal("forged certificate for bank.com verified; name constraints are not working")
	}
}

func TestReloadKeepsSameRoot(t *testing.T) {
	dir := t.TempDir()
	a, _ := Load(dir, "test")
	b, err := Load(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if !a.cert.Equal(b.cert) {
		t.Fatal("CA changed on reload")
	}
}

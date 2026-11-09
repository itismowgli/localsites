// Package ca is LocalSites' certificate authority.
//
// The root certificate carries X.509 name constraints: it can only sign names under
// the local TLD (".test"), "localhost" and loopback IPs. Even if its key leaked, it
// could not be used to impersonate real websites (unlike general dev CAs).
package ca

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
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
	"strings"
	"sync"
	"time"
)

type CA struct {
	dir  string
	tld  string
	cert *x509.Certificate
	key  crypto.Signer

	mu    sync.Mutex
	cache map[string]*tls.Certificate
}

func (c *CA) RootPath() string { return filepath.Join(c.dir, "root.crt") }
func (c *CA) keyPath() string  { return filepath.Join(c.dir, "root.key") }

// Load opens the CA in dir, creating it on first use.
func Load(dir, tld string) (*CA, error) {
	c := &CA{dir: dir, tld: tld, cache: map[string]*tls.Certificate{}}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Stat(c.RootPath()); errors.Is(err, os.ErrNotExist) {
		if err := c.create(); err != nil {
			return nil, err
		}
	}
	return c, c.load()
}

func (c *CA) create() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber:                randSerial(),
		Subject:                     pkix.Name{CommonName: "LocalSites Development CA", Organization: []string{"LocalSites (" + host + ")"}},
		NotBefore:                   time.Now().Add(-time.Hour),
		NotAfter:                    time.Now().AddDate(10, 0, 0),
		IsCA:                        true,
		BasicConstraintsValid:       true,
		MaxPathLenZero:              true,
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{"." + c.tld, c.tld, "localhost"},
		PermittedIPRanges: []*net.IPNet{
			{IP: net.ParseIP("127.0.0.0").To4(), Mask: net.CIDRMask(8, 32)},
			{IP: net.ParseIP("::1"), Mask: net.CIDRMask(128, 128)},
		},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	kb, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(c.keyPath(), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(c.RootPath(), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

func (c *CA) load() error {
	cb, err := os.ReadFile(c.RootPath())
	if err != nil {
		return err
	}
	kb, err := os.ReadFile(c.keyPath())
	if err != nil {
		return err
	}
	cp, _ := pem.Decode(cb)
	kp, _ := pem.Decode(kb)
	if cp == nil || kp == nil {
		return errors.New("CA files are damaged")
	}
	if c.cert, err = x509.ParseCertificate(cp.Bytes); err != nil {
		return err
	}
	k, err := x509.ParsePKCS8PrivateKey(kp.Bytes)
	if err != nil {
		return err
	}
	signer, ok := k.(crypto.Signer)
	if !ok {
		return errors.New("CA key cannot sign")
	}
	c.key = signer
	return nil
}

// Allowed reports whether this CA may issue for name.
func (c *CA) Allowed(name string) bool {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if ip := net.ParseIP(name); ip != nil {
		return ip.IsLoopback()
	}
	return name == "localhost" || strings.HasSuffix(name, "."+c.tld)
}

// Issue creates (or returns cached) a leaf certificate for names.
func (c *CA) Issue(names ...string) (*tls.Certificate, error) {
	key := strings.Join(names, ",")
	c.mu.Lock()
	defer c.mu.Unlock()
	if tc, ok := c.cache[key]; ok && time.Until(tc.Leaf.NotAfter) > 7*24*time.Hour {
		return tc, nil
	}
	tmpl := &x509.Certificate{
		SerialNumber: randSerial(),
		Subject:      pkix.Name{CommonName: names[0]},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(0, 0, 397), // browsers reject longer leaf lifetimes
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, n := range names {
		if !c.Allowed(n) {
			return nil, fmt.Errorf("refusing to issue a certificate for %q (only *.%s, localhost and loopback IPs)", n, c.tld)
		}
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	lk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &lk.PublicKey, c.key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	tc := &tls.Certificate{Certificate: [][]byte{der, c.cert.Raw}, PrivateKey: lk, Leaf: leaf}
	c.cache[key] = tc
	return tc, nil
}

// WritePair writes a leaf cert + key as PEM files (for Apache, MySQL).
func (c *CA) WritePair(certPath, keyPath string, names ...string) error {
	tc, err := c.Issue(names...)
	if err != nil {
		return err
	}
	var chain []byte
	for _, d := range tc.Certificate {
		chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d})...)
	}
	kb, err := x509.MarshalPKCS8PrivateKey(tc.PrivateKey)
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(certPath, chain, 0o644)
}

// GetCertificate is a tls.Config hook issuing certs on demand by SNI.
func (c *CA) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := hello.ServerName
	if name == "" {
		name = "localhost"
	}
	return c.Issue(name)
}

// Pool returns a cert pool trusting only this CA (for internal clients and tests).
func (c *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(c.cert)
	return p
}

func randSerial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 126))
	return n
}

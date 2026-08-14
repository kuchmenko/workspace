package syncservice

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

type Identity struct {
	ca          *x509.Certificate
	caKey       ed25519.PrivateKey
	caPEM       []byte
	serverCert  tls.Certificate
	fingerprint string
}

func OpenIdentity(stateDir string, advertisedNames []string) (*Identity, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(stateDir, 0o700); err != nil {
		return nil, err
	}
	ca, caKey, caPEM, err := loadOrCreateCA(stateDir)
	if err != nil {
		return nil, err
	}
	serverCert, err := loadOrCreateServerCertificate(stateDir, ca, caKey, advertisedNames)
	if err != nil {
		return nil, err
	}
	fingerprint := sha256.Sum256(ca.Raw)
	return &Identity{ca: ca, caKey: caKey, caPEM: caPEM, serverCert: serverCert, fingerprint: hex.EncodeToString(fingerprint[:])}, nil
}

func loadOrCreateServerCertificate(stateDir string, ca *x509.Certificate, caKey ed25519.PrivateKey, names []string) (tls.Certificate, error) {
	certPath, keyPath := filepath.Join(stateDir, "server.pem"), filepath.Join(stateDir, "server-key.pem")
	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	if certErr == nil && keyErr == nil {
		certificateChain := append(append([]byte(nil), certPEM...), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})...)
		certificate, err := tls.X509KeyPair(certificateChain, keyPEM)
		if err != nil {
			return tls.Certificate{}, err
		}
		return certificate, nil
	}
	if !errors.Is(certErr, os.ErrNotExist) || !errors.Is(keyErr, os.ErrNotExist) {
		return tls.Certificate{}, errors.New("incomplete server identity")
	}
	return createServerCertificate(stateDir, ca, caKey, names)
}

func loadOrCreateCA(stateDir string) (*x509.Certificate, ed25519.PrivateKey, []byte, error) {
	certPath, keyPath := filepath.Join(stateDir, "ca.pem"), filepath.Join(stateDir, "ca-key.pem")
	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	if certErr == nil && keyErr == nil {
		return parseCA(certPEM, keyPEM)
	}
	if !errors.Is(certErr, os.ErrNotExist) || !errors.Is(keyErr, os.ErrNotExist) {
		return nil, nil, nil, errors.New("incomplete identity")
	}
	return createCA(certPath, keyPath)
}

func parseCA(certPEM, keyPEM []byte) (*x509.Certificate, ed25519.PrivateKey, []byte, error) {
	certBlock, _ := pem.Decode(certPEM)
	keyBlock, _ := pem.Decode(keyPEM)
	if certBlock == nil || keyBlock == nil {
		return nil, nil, nil, errors.New("invalid identity PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, nil, err
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	key, ok := keyAny.(ed25519.PrivateKey)
	if err != nil || !ok {
		return nil, nil, nil, errors.New("invalid Ed25519 CA key")
	}
	return cert, key, certPEM, nil
}

func createCA(certPath, keyPath string) (*x509.Certificate, ed25519.PrivateKey, []byte, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	now := time.Now().UTC()
	template := &x509.Certificate{SerialNumber: randomSerial(), Subject: pkix.Name{CommonName: "ws LAN sync CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(20, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		return nil, nil, nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, nil, nil, err
	}
	if err = writePrivateFile(certPath, certPEM); err != nil {
		return nil, nil, nil, err
	}
	if err = writePrivateFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})); err != nil {
		return nil, nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return cert, privateKey, certPEM, err
}

func createServerCertificate(stateDir string, ca *x509.Certificate, caKey ed25519.PrivateKey, names []string) (tls.Certificate, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	dnsNames := []string{"localhost"}
	ipAddresses := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	for _, name := range names {
		if ip := net.ParseIP(name); ip != nil {
			ipAddresses = append(ipAddresses, ip)
		} else if name != "" {
			dnsNames = append(dnsNames, name)
		}
	}
	now := time.Now().UTC()
	template := &x509.Certificate{SerialNumber: randomSerial(), Subject: pkix.Name{CommonName: "ws LAN sync"}, DNSNames: dnsNames, IPAddresses: ipAddresses, NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, publicKey, caKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	certificateChain := append(append([]byte(nil), certPEM...), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})...)
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err = writePrivateFile(filepath.Join(stateDir, "server.pem"), certPEM); err != nil {
		return tls.Certificate{}, err
	}
	if err = writePrivateFile(filepath.Join(stateDir, "server-key.pem"), keyPEM); err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(certificateChain, keyPEM)
}

func writePrivateFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func randomSerial() *big.Int {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		panic(err)
	}
	return serial
}

func (i *Identity) Fingerprint() string { return i.fingerprint }

func (i *Identity) CAPEM() []byte { return append([]byte(nil), i.caPEM...) }

func (i *Identity) ServerTLSConfig() *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(i.ca)
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{i.serverCert}, ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: pool}
}

func (i *Identity) ClientTLSConfig(serverName string, certificate tls.Certificate) *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(i.ca)
	return &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: pool, ServerName: serverName, Certificates: []tls.Certificate{certificate}}
}

func (i *Identity) issueClient(csrPEM, actorID string) ([]byte, string, string, error) {
	block, _ := pem.Decode([]byte(csrPEM))
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, "", "", errors.New("invalid CSR PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil {
		return nil, "", "", errors.New("invalid CSR")
	}
	publicDER, err := x509.MarshalPKIXPublicKey(csr.PublicKey)
	if err != nil {
		return nil, "", "", err
	}
	publicHash := sha256.Sum256(publicDER)
	now := time.Now().UTC()
	template := &x509.Certificate{SerialNumber: randomSerial(), Subject: pkix.Name{CommonName: actorID}, NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, i.ca, csr.PublicKey, i.caKey)
	if err != nil {
		return nil, "", "", err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), template.SerialNumber.String(), fmt.Sprintf("%x", publicHash), nil
}

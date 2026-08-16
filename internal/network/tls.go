package network

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/kuchmenko/workspace/internal/device"
)

func certificate(identity device.Identity, name string) (tls.Certificate, error) {
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, identity.PublicKey(), identity.Signer())
	if err != nil {
		return tls.Certificate{}, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: identity.Signer(), Leaf: leaf}, nil
}

func pairingServerTLS(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		ClientAuth:   tls.RequireAnyClientCert,
	}
}

func pairingClientTLS(cert tls.Certificate, peer *x509.Certificate) *tls.Config {
	roots := x509.NewCertPool()
	roots.AddCert(peer)
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		ServerName:   peer.Subject.CommonName,
	}
}

func peerServerTLS(cert tls.Certificate, trusted func(string) bool) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		ClientAuth:   tls.RequireAnyClientCert,
		VerifyConnection: func(state tls.ConnectionState) error {
			public, _, err := peerPublicKey(state)
			if err != nil {
				return err
			}
			if !trusted(device.IDForPublicKey(public)) {
				return errors.New("peer is not an active network device")
			}
			return nil
		},
	}
}

func peerClientTLS(cert tls.Certificate, expected ed25519.PublicKey, name string) (*tls.Config, error) {
	signer, ok := cert.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, errors.New("local TLS key cannot sign certificates")
	}
	root, err := publicKeyRoot(expected, name, signer)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		ServerName:   name,
		VerifyConnection: func(state tls.ConnectionState) error {
			public, _, err := peerPublicKey(state)
			if err != nil {
				return err
			}
			if !public.Equal(expected) {
				return errors.New("peer certificate does not match paired identity")
			}
			return nil
		},
	}, nil
}

func publicKeyRoot(public ed25519.PublicKey, name string, signer crypto.Signer) (*x509.Certificate, error) {
	template := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, public, signer)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

func peerPublicKey(state tls.ConnectionState) (ed25519.PublicKey, string, error) {
	if len(state.PeerCertificates) != 1 {
		return nil, "", errors.New("peer must present exactly one certificate")
	}
	public, ok := state.PeerCertificates[0].PublicKey.(ed25519.PublicKey)
	if !ok {
		return nil, "", errors.New("peer identity is not Ed25519")
	}
	return append(ed25519.PublicKey(nil), public...), state.PeerCertificates[0].Subject.CommonName, nil
}

func authenticationString(left, right ed25519.PublicKey) string {
	keys := [][]byte{left, right}
	sort.Slice(keys, func(left, right int) bool { return string(keys[left]) < string(keys[right]) })
	digest := sha256.Sum256(append(append([]byte(nil), keys[0]...), keys[1]...))
	number := uint32(digest[0])<<16 | uint32(digest[1])<<8 | uint32(digest[2])
	return fmt.Sprintf("%03d-%03d", number/1000%1000, number%1000)
}

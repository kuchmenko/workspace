package syncclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Credentials struct {
	Endpoint      string `json:"endpoint"`
	ServiceID     string `json:"service_id"`
	ServiceEpoch  string `json:"service_epoch"`
	ActorID       string `json:"actor_id"`
	Role          string `json:"role"`
	CAPEM         []byte `json:"ca_pem"`
	ClientCertPEM []byte `json:"client_cert_pem"`
	ClientKeyPEM  []byte `json:"client_key_pem"`
}

type PairingCode struct {
	Endpoint      string `json:"endpoint"`
	ServiceID     string `json:"service_id"`
	CAFingerprint string `json:"ca_sha256"`
	Token         string `json:"token"`
}

type pairingRequest struct {
	AttemptID   string `json:"attempt_id"`
	Token       string `json:"token"`
	DisplayName string `json:"display_name"`
	CSR         string `json:"csr"`
}

type pairingResponse struct {
	ServiceID    string `json:"service_id"`
	ServiceEpoch string `json:"service_epoch"`
	ActorID      string `json:"actor_id"`
	Role         string `json:"role"`
	CAPEM        string `json:"ca_pem"`
	Certificate  string `json:"certificate_pem"`
}

type pairingAttempt struct {
	Code       PairingCode    `json:"code"`
	Request    pairingRequest `json:"request"`
	PrivateKey []byte         `json:"private_key"`
}

func NewPairingCode(endpoint, serviceID string, caPEM []byte) (PairingCode, error) {
	if _, err := pairingEndpoint(endpoint); err != nil {
		return PairingCode{}, err
	}
	block, _ := pem.Decode(caPEM)
	if block == nil {
		return PairingCode{}, errors.New("invalid CA PEM")
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return PairingCode{}, err
	}
	sum := sha256.Sum256(block.Bytes)
	return PairingCode{Endpoint: strings.TrimRight(endpoint, "/"), ServiceID: serviceID, CAFingerprint: hex.EncodeToString(sum[:]), Token: base64.RawURLEncoding.EncodeToString(token)}, nil
}

func (c PairingCode) String() string {
	b, _ := json.Marshal(c)
	return "ws1:" + base64.RawURLEncoding.EncodeToString(b)
}

func ParsePairingCode(value string) (PairingCode, error) {
	if !strings.HasPrefix(value, "ws1:") {
		return PairingCode{}, errors.New("invalid pairing code prefix")
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "ws1:"))
	if err != nil {
		return PairingCode{}, err
	}
	var code PairingCode
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&code); err != nil {
		return PairingCode{}, err
	}
	if code.Endpoint == "" || code.ServiceID == "" || len(code.CAFingerprint) != 64 || code.Token == "" {
		return PairingCode{}, errors.New("incomplete pairing code")
	}
	if _, err = pairingEndpoint(code.Endpoint); err != nil {
		return PairingCode{}, err
	}
	return code, nil
}

func pairingEndpoint(endpoint string) (*url.URL, error) {
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.Port() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("pairing endpoint must be an HTTPS origin with an explicit port")
	}
	return parsed, nil
}

func Pair(ctx context.Context, code PairingCode) (Credentials, error) {
	return pairAttempt(ctx, newPairingAttempt(code, "ws client"))
}

func PairPersistent(ctx context.Context, code PairingCode, displayName, attemptPath string) (Credentials, error) {
	attempt, err := loadPairingAttempt(attemptPath)
	if errors.Is(err, os.ErrNotExist) {
		attempt = newPairingAttempt(code, displayName)
		if attempt.Request.AttemptID == "" {
			return Credentials{}, errors.New("creating pairing attempt failed")
		}
		if err = savePrivateJSON(attemptPath, attempt); err != nil {
			return Credentials{}, err
		}
	} else if err != nil {
		return Credentials{}, err
	} else if attempt.Code != code {
		return Credentials{}, errors.New("a different pairing attempt is pending")
	}
	return pairAttempt(ctx, attempt)
}

func newPairingAttempt(code PairingCode, displayName string) pairingAttempt {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return pairingAttempt{}
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "ws client"}}, privateKey)
	if err != nil {
		return pairingAttempt{}
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return pairingAttempt{}
	}
	request := pairingRequest{AttemptID: base64.RawURLEncoding.EncodeToString(csrDER[:18]), Token: code.Token, DisplayName: displayName, CSR: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}))}
	return pairingAttempt{Code: code, Request: request, PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})}
}

func pairAttempt(ctx context.Context, attempt pairingAttempt) (Credentials, error) {
	code := attempt.Code
	endpoint, err := pairingEndpoint(code.Endpoint)
	if err != nil {
		return Credentials{}, err
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true}
	tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return errors.New("server presented no certificate")
		}
		var pinned *x509.Certificate
		for _, certificate := range state.PeerCertificates {
			sum := sha256.Sum256(certificate.Raw)
			if strings.EqualFold(hex.EncodeToString(sum[:]), code.CAFingerprint) {
				pinned = certificate
				break
			}
		}
		if pinned == nil || !pinned.IsCA {
			return errors.New("pairing CA fingerprint mismatch")
		}
		roots := x509.NewCertPool()
		roots.AddCert(pinned)
		intermediates := x509.NewCertPool()
		for _, certificate := range state.PeerCertificates[1:] {
			if !certificate.Equal(pinned) {
				intermediates.AddCert(certificate)
			}
		}
		_, err := state.PeerCertificates[0].Verify(x509.VerifyOptions{DNSName: endpoint.Hostname(), Roots: roots, Intermediates: intermediates})
		return err
	}
	var response pairingResponse
	if err := jsonCall(ctx, &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}}, http.MethodPost, code.Endpoint+"/v1/pair", attempt.Request, &response); err != nil {
		return Credentials{}, err
	}
	if response.ServiceID != code.ServiceID {
		return Credentials{}, &IdentityError{WantServiceID: code.ServiceID, GotServiceID: response.ServiceID, GotEpoch: response.ServiceEpoch}
	}
	block, _ := pem.Decode([]byte(response.CAPEM))
	if block == nil {
		return Credentials{}, errors.New("invalid pairing CA PEM")
	}
	sum := sha256.Sum256(block.Bytes)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), code.CAFingerprint) {
		return Credentials{}, errors.New("pairing CA fingerprint mismatch")
	}
	if _, err := tls.X509KeyPair([]byte(response.Certificate), attempt.PrivateKey); err != nil {
		return Credentials{}, errors.New("pairing certificate does not match generated key")
	}
	return Credentials{Endpoint: code.Endpoint, ServiceID: response.ServiceID, ServiceEpoch: response.ServiceEpoch, ActorID: response.ActorID, Role: response.Role, CAPEM: []byte(response.CAPEM), ClientCertPEM: []byte(response.Certificate), ClientKeyPEM: attempt.PrivateKey}, nil
}

func loadPairingAttempt(path string) (pairingAttempt, error) {
	var attempt pairingAttempt
	b, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(b, &attempt)
	}
	return attempt, err
}

func savePrivateJSON(path string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func SaveCredentials(path string, credentials Credentials) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(directory, ".credentials-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0o600); err == nil {
		err = json.NewEncoder(f).Encode(credentials)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	d, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func LoadCredentials(path string) (Credentials, error) {
	f, err := os.Open(path)
	if err != nil {
		return Credentials{}, err
	}
	defer f.Close()
	var credentials Credentials
	decoder := json.NewDecoder(io.LimitReader(f, maxResponseBytes+1))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&credentials); err != nil {
		return Credentials{}, err
	}
	return credentials, nil
}

func (c Credentials) HTTPClient() (*http.Client, error) {
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(c.CAPEM) {
		return nil, errors.New("invalid CA PEM")
	}
	certificate, err := tls.X509KeyPair(c.ClientCertPEM, c.ClientKeyPEM)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{certificate}}}}, nil
}

func jsonCall(ctx context.Context, client *http.Client, method, endpoint string, input, output any) error {
	var body io.Reader
	if input != nil {
		reader, writer := io.Pipe()
		go func() { writer.CloseWithError(json.NewEncoder(writer).Encode(input)) }()
		body = reader
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maxResponseBytes+1)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(limited, 4096))
		return fmt.Errorf("service returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	if output == nil {
		_, err = io.Copy(io.Discard, limited)
		return err
	}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(output); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("invalid trailing response data")
	}
	return nil
}

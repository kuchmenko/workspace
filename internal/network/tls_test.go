package network

import (
	"context"
	"crypto/tls"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/device"
)

func TestPeerServerTLSRenewsExpiringCertificate(t *testing.T) {
	identity, err := device.Load(t.TempDir() + "/identity.key")
	if err != nil {
		t.Fatal(err)
	}
	cert, err := peerCertificate(identity, "arch")
	if err != nil {
		t.Fatal(err)
	}
	cert.Leaf.NotAfter = time.Now()
	config := peerServerTLS(cert, identity, "arch", func(string) bool { return true })
	renewed, err := config.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(renewed.Leaf.NotAfter) < 23*time.Hour {
		t.Fatalf("renewed certificate expires at %s", renewed.Leaf.NotAfter)
	}
}

func TestPeerTLSAuthenticatesPinnedIdentityAfterRuntimeNameChanges(t *testing.T) {
	serverIdentity, err := device.Load(t.TempDir() + "/server.key")
	if err != nil {
		t.Fatal(err)
	}
	clientIdentity, err := device.Load(t.TempDir() + "/client.key")
	if err != nil {
		t.Fatal(err)
	}
	serverCertificate, err := peerCertificate(serverIdentity, "runtime-name")
	if err != nil {
		t.Fatal(err)
	}
	clientCertificate, err := peerCertificate(clientIdentity, "client")
	if err != nil {
		t.Fatal(err)
	}
	serverConfig := peerServerTLS(serverCertificate, serverIdentity, "runtime-name", func(id string) bool {
		return id == clientIdentity.ID()
	})
	clientConfig, err := peerClientTLS(clientCertificate, serverIdentity.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	serverResult := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverResult <- acceptErr
			return
		}
		defer connection.Close()
		serverResult <- tls.Server(connection, serverConfig).HandshakeContext(ctx)
	}()
	dialer := tls.Dialer{Config: clientConfig}
	connection, err := dialer.DialContext(ctx, "tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("client handshake after runtime name change: %v", err)
	}
	defer connection.Close()
	if err = <-serverResult; err != nil {
		t.Fatalf("server handshake after runtime name change: %v", err)
	}
}

func TestDecodeLimitedRejectsOversizedJSON(t *testing.T) {
	var value string
	err := decodeLimited(strings.NewReader(`"`+strings.Repeat("x", 1024)+`"`), 128, &value)
	if err == nil {
		t.Fatal("oversized JSON decoded")
	}
}

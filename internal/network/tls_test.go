package network

import (
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
	cert, err := certificate(identity, "arch")
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

func TestDecodeLimitedRejectsOversizedJSON(t *testing.T) {
	var value string
	err := decodeLimited(strings.NewReader(`"`+strings.Repeat("x", 1024)+`"`), 128, &value)
	if err == nil {
		t.Fatal("oversized JSON decoded")
	}
}

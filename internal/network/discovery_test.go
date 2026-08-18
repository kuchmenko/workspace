package network

import (
	"net"
	"strings"
	"testing"

	"github.com/grandcat/zeroconf"
)

func TestPairDiscoveryUsesInvitationIdentifier(t *testing.T) {
	code := "0123456789abcdef-123456"
	identifier := invitationID(code)
	if identifier == code || strings.Contains(identifier, code) {
		t.Fatalf("invitation identifier exposes pairing code: %q", identifier)
	}
	entry := &zeroconf.ServiceEntry{Text: []string{"v=1", "invitation=" + identifier}}
	if !entryHasCode(entry, code) {
		t.Fatal("matching code did not find invitation")
	}
	if entryHasCode(entry, "fedcba9876543210-123456") {
		t.Fatal("different code matched invitation")
	}
	legacy := &zeroconf.ServiceEntry{Text: []string{"v=1", "code=" + code}}
	if entryHasCode(legacy, code) {
		t.Fatal("plaintext pairing code advertisement was accepted")
	}
	if invitationID("123456") != "" {
		t.Fatal("legacy low-entropy code produced an invitation identifier")
	}
}

func TestDiscoveryEndpointPreservesIPv6LinkLocalScope(t *testing.T) {
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	var received net.Interface
	for _, candidate := range interfaces {
		if candidate.Index > 0 && candidate.Name != "" {
			received = candidate
			break
		}
	}
	if received.Index == 0 {
		t.Fatal("no network interface available")
	}
	entry := &zeroconf.ServiceEntry{
		Port:            17337,
		AddrIPv6:        []net.IP{net.ParseIP("fe80::1")},
		ReceivedIfIndex: received.Index,
	}
	want := net.JoinHostPort("fe80::1%"+received.Name, "17337")
	if got := discoveryEndpoint(entry); got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}

func TestDiscoveryEndpointsRetainsEveryAddress(t *testing.T) {
	entry := &zeroconf.ServiceEntry{
		Port:     17337,
		AddrIPv4: []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("192.0.2.2")},
		AddrIPv6: []net.IP{net.ParseIP("2001:db8::1")},
	}
	want := []string{"192.0.2.1:17337", "192.0.2.2:17337", "[2001:db8::1]:17337"}
	got := discoveryEndpoints(entry)
	if len(got) != len(want) {
		t.Fatalf("endpoints = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("endpoints = %v, want %v", got, want)
		}
	}
}

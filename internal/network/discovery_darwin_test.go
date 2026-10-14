package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/grandcat/zeroconf"
)

func TestNativeBonjourPairDiscoveryAndCleanup(t *testing.T) {
	code := fmt.Sprintf("%016x-123456", time.Now().UnixNano())
	ad, err := advertisePair("ws-test-"+invitationID(code), code, 18123)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if ad != nil {
			ad.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	endpoint, err := discoverPair(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil || port != "18123" {
		t.Fatalf("endpoint = %q, error = %v", endpoint, err)
	}
	host, _, _ = strings.Cut(host, "%")
	if net.ParseIP(host) == nil {
		t.Fatalf("endpoint has no numeric address: %q", endpoint)
	}
	ad.Close()
	ad = nil
	deadline := time.Now().Add(2 * time.Second)
	for {
		output, err := exec.Command("/usr/bin/pgrep", "-P", strconv.Itoa(os.Getpid()), "-x", "dns-sd").CombinedOutput()
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() == 1 {
				break
			}
			t.Fatalf("check child processes: %v: %s", err, output)
		}
		if time.Now().After(deadline) {
			t.Fatalf("Bonjour processes remain after cleanup: %s", output)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestNativeBonjourAddressOutputPreservesScope(t *testing.T) {
	interfaces, err := net.Interfaces()
	if err != nil || len(interfaces) == 0 {
		t.Fatalf("interfaces = %v, error = %v", interfaces, err)
	}
	iface := interfaces[0]
	service := &zeroconf.ServiceEntry{Port: 17337}
	for _, test := range []struct{ address, want string }{
		{"FDE5:5D3B:E270:40BF:759D:06E0:05E2:0CC4%<0>", "[fde5:5d3b:e270:40bf:759d:6e0:5e2:cc4]:17337"},
		{"FE80:0000:0000:0000:C938:BCFC:CFC9:D878%" + iface.Name, net.JoinHostPort("fe80::c938:bcfc:cfc9:d878%"+iface.Name, "17337")},
		{"192.168.88.69", "192.168.88.69:17337"},
	} {
		line := fmt.Sprintf("11:12:14.928 Add 40000003 %d peer.local. %s 120", iface.Index, test.address)
		entry := nativeAddressRecord(line, service)
		if entry == nil || discoveryEndpoint(entry) != test.want {
			t.Fatalf("address %q: entry=%#v, want %q", test.address, entry, test.want)
		}
	}
	for _, line := range []string{
		"Timestamp A/R Flags IF Hostname Address TTL",
		"11:12:16.364 Rmv 1 14 peer.local. 192.0.2.1 0",
		"11:12:16.364 Add 1 14 peer.local. 0.0.0.0 0 No Such Record",
	} {
		if entry := nativeAddressRecord(line, service); entry != nil {
			t.Fatalf("non-address result accepted: %q: %#v", line, entry)
		}
	}
}

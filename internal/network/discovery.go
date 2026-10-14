package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/grandcat/zeroconf"
)

const pairingService = "_ws-pair._tcp"
const peerService = "_ws-peer._tcp"

type advertisement struct {
	close func()
}

func advertisePair(instance, code string, port int) (*advertisement, error) {
	return advertiseService(instance, pairingService, port, []string{"v=1", "invitation=" + invitationID(code)})
}

func advertisePeer(instance, id string, port int) (*advertisement, error) {
	return advertiseService(instance, peerService, port, []string{"v=1", "id=" + id})
}

func (advertisement *advertisement) Close() {
	advertisement.close()
}

func discoverPair(ctx context.Context, code string) (string, error) {
	browseContext, cancel := context.WithCancel(ctx)
	defer cancel()
	entries, err := browseServices(browseContext, pairingService)
	if err != nil {
		return "", err
	}
	for entry := range entries {
		if !entryHasCode(entry, code) {
			continue
		}
		if endpoint := discoveryEndpoint(entry); endpoint != "" {
			return endpoint, nil
		}
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	return "", errors.New("pairing device disappeared")
}

func entryHasCode(entry *zeroconf.ServiceEntry, code string) bool {
	version := false
	matched := false
	for _, value := range entry.Text {
		key, content, found := strings.Cut(value, "=")
		if !found {
			continue
		}
		switch key {
		case "v":
			version = content == "1"
		case "invitation":
			matched = content == invitationID(code)
		}
	}
	return version && matched
}

func invitationID(code string) string {
	invitation, _, found := strings.Cut(code, "-")
	if !found || len(invitation) != 16 {
		return ""
	}
	return invitation
}

func discoverPeers(ctx context.Context) (map[string][]string, error) {
	browseContext, cancel := context.WithCancel(ctx)
	defer cancel()
	entries, err := browseServices(browseContext, peerService)
	if err != nil {
		return normalizePeerDiscovery(ctx, nil, err)
	}
	peers := map[string][]string{}
	for entry := range entries {
		id := entryValue(entry, "id")
		if entryValue(entry, "v") != "1" || id == "" {
			continue
		}
		for _, endpoint := range discoveryEndpoints(entry) {
			if !containsEndpoint(peers[id], endpoint) {
				peers[id] = append(peers[id], endpoint)
			}
		}
	}
	if err = ctx.Err(); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	return peers, nil
}

func normalizePeerDiscovery(ctx context.Context, peers map[string][]string, err error) (map[string][]string, error) {
	if err == nil {
		return peers, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	return map[string][]string{}, nil
}

func discoveryEndpoint(entry *zeroconf.ServiceEntry) string {
	endpoints := discoveryEndpoints(entry)
	if len(endpoints) == 0 {
		return ""
	}
	return endpoints[0]
}

func discoveryEndpoints(entry *zeroconf.ServiceEntry) []string {
	port := strconv.Itoa(entry.Port)
	endpoints := make([]string, 0, len(entry.AddrIPv4)+len(entry.AddrIPv6))
	for _, address := range entry.AddrIPv4 {
		endpoints = append(endpoints, net.JoinHostPort(address.String(), port))
	}
	for _, address := range entry.AddrIPv6 {
		host := address.String()
		if address.IsLinkLocalUnicast() {
			networkInterface, err := net.InterfaceByIndex(entry.ReceivedIfIndex)
			if err != nil {
				continue
			}
			host += "%" + networkInterface.Name
		}
		endpoints = append(endpoints, net.JoinHostPort(host, port))
	}
	return endpoints
}

func containsEndpoint(endpoints []string, wanted string) bool {
	for _, endpoint := range endpoints {
		if endpoint == wanted {
			return true
		}
	}
	return false
}

func entryValue(entry *zeroconf.ServiceEntry, wanted string) string {
	for _, value := range entry.Text {
		key, content, found := strings.Cut(value, "=")
		if found && key == wanted {
			return content
		}
	}
	return ""
}

func discoveryInstance(name, id string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "ws"
	}
	if len(id) > 8 {
		id = id[:8]
	}
	return fmt.Sprintf("%s-%s", name, id)
}

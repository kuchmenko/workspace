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
	server *zeroconf.Server
}

func advertisePair(instance, code string, port int) (*advertisement, error) {
	server, err := zeroconf.Register(instance, pairingService, "local.", port, []string{"v=1", "invitation=" + invitationID(code)}, nil)
	if err != nil {
		return nil, err
	}
	return &advertisement{server: server}, nil
}

func advertisePeer(instance, id string, port int) (*advertisement, error) {
	server, err := zeroconf.Register(instance, peerService, "local.", port, []string{"v=1", "id=" + id}, nil)
	if err != nil {
		return nil, err
	}
	return &advertisement{server: server}, nil
}

func (advertisement *advertisement) Close() {
	advertisement.server.Shutdown()
}

func discoverPair(ctx context.Context, code string) (string, error) {
	browseContext, cancel := context.WithCancel(ctx)
	defer cancel()
	resolver, err := zeroconf.NewResolver()
	if err != nil {
		return "", err
	}
	entries := make(chan *zeroconf.ServiceEntry)
	if err = resolver.Browse(browseContext, pairingService, "local.", entries); err != nil {
		return "", err
	}
	for entry := range entries {
		if !entryHasCode(entry, code) {
			continue
		}
		if len(entry.AddrIPv4) > 0 {
			return net.JoinHostPort(entry.AddrIPv4[0].String(), strconv.Itoa(entry.Port)), nil
		}
		if len(entry.AddrIPv6) > 0 {
			return net.JoinHostPort(entry.AddrIPv6[0].String(), strconv.Itoa(entry.Port)), nil
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

func discoverPeers(ctx context.Context) (map[string]string, error) {
	browseContext, cancel := context.WithCancel(ctx)
	defer cancel()
	resolver, err := zeroconf.NewResolver()
	if err != nil {
		return nil, err
	}
	entries := make(chan *zeroconf.ServiceEntry)
	if err = resolver.Browse(browseContext, peerService, "local.", entries); err != nil {
		return nil, err
	}
	peers := map[string]string{}
	for entry := range entries {
		id := entryValue(entry, "id")
		if entryValue(entry, "v") != "1" || id == "" {
			continue
		}
		if len(entry.AddrIPv4) > 0 {
			peers[id] = net.JoinHostPort(entry.AddrIPv4[0].String(), strconv.Itoa(entry.Port))
		} else if len(entry.AddrIPv6) > 0 {
			peers[id] = net.JoinHostPort(entry.AddrIPv6[0].String(), strconv.Itoa(entry.Port))
		}
	}
	if err = ctx.Err(); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	return peers, nil
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

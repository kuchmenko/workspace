//go:build !darwin

package network

import (
	"context"

	"github.com/grandcat/zeroconf"
)

func advertiseService(instance, service string, port int, txt []string) (*advertisement, error) {
	server, err := zeroconf.Register(instance, service, "local.", port, txt, nil)
	if err != nil {
		return nil, err
	}
	return &advertisement{close: server.Shutdown}, nil
}

func browseServices(ctx context.Context, service string) (<-chan *zeroconf.ServiceEntry, error) {
	resolver, err := zeroconf.NewResolver()
	if err != nil {
		return nil, err
	}
	entries := make(chan *zeroconf.ServiceEntry)
	if err = resolver.Browse(ctx, service, "local.", entries); err != nil {
		return nil, err
	}
	return entries, nil
}

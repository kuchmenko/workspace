package network

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

const nativeDNSSD = "/usr/bin/dns-sd"

func advertiseService(instance, service string, port int, txt []string) (*advertisement, error) {
	ctx, cancel := context.WithCancel(context.Background())
	args := append([]string{"-R", instance, service, "local.", strconv.Itoa(port)}, txt...)
	command := exec.CommandContext(ctx, nativeDNSSD, args...)
	output, err := command.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err = command.Start(); err != nil {
		cancel()
		return nil, err
	}
	ready := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			if strings.HasSuffix(scanner.Text(), "Name now registered and active") {
				select {
				case ready <- struct{}{}:
				default:
				}
			}
		}
		done <- command.Wait()
	}()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-ready:
		return &advertisement{close: func() { cancel(); <-done }}, nil
	case err = <-done:
		cancel()
		return nil, fmt.Errorf("bonjour registration stopped: %w", errors.Join(err, errors.New("service was not registered")))
	case <-timer.C:
		cancel()
		<-done
		return nil, errors.New("bonjour registration timed out")
	}
}

func browseServices(ctx context.Context, service string) (<-chan *zeroconf.ServiceEntry, error) {
	command := exec.CommandContext(ctx, nativeDNSSD, "-Z", service, "local.")
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = command.Start(); err != nil {
		return nil, err
	}
	entries := make(chan *zeroconf.ServiceEntry)
	go func() {
		defer close(entries)
		var addresses sync.WaitGroup
		records := map[string]*zeroconf.ServiceEntry{}
		seen := map[string]bool{}
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			host, entry := nativeServiceRecord(scanner.Text(), records)
			if host == "" || entryValue(entry, "v") != "1" {
				continue
			}
			key := host + strconv.Itoa(entry.Port) + strings.Join(entry.Text, "\x00")
			if seen[key] {
				continue
			}
			seen[key] = true
			addresses.Add(1)
			go func() {
				defer addresses.Done()
				browseNativeAddresses(ctx, host, entry, entries)
			}()
		}
		_ = command.Wait()
		addresses.Wait()
	}()
	return entries, nil
}

func nativeServiceRecord(line string, records map[string]*zeroconf.ServiceEntry) (string, *zeroconf.ServiceEntry) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return "", nil
	}
	switch fields[1] {
	case "SRV":
		if len(fields) < 6 {
			return "", nil
		}
		port, err := strconv.ParseUint(fields[4], 10, 16)
		if err == nil && port > 0 {
			records[fields[0]] = &zeroconf.ServiceEntry{HostName: fields[5], Port: int(port)}
		}
	case "TXT":
		record := records[fields[0]]
		if record == nil {
			return "", nil
		}
		entry := *record
		for _, field := range fields[2:] {
			value, err := strconv.Unquote(field)
			if err == nil {
				entry.Text = append(entry.Text, value)
			}
		}
		return entry.HostName, &entry
	}
	return "", nil
}

func browseNativeAddresses(ctx context.Context, host string, service *zeroconf.ServiceEntry, entries chan<- *zeroconf.ServiceEntry) {
	command := exec.CommandContext(ctx, nativeDNSSD, "-G", "v4v6", host)
	output, err := command.StdoutPipe()
	if err != nil {
		return
	}
	if err = command.Start(); err != nil {
		return
	}
	scanner := bufio.NewScanner(output)
	for scanner.Scan() {
		entry := nativeAddressRecord(scanner.Text(), service)
		if entry == nil {
			continue
		}
		select {
		case entries <- entry:
		case <-ctx.Done():
		}
	}
	_ = command.Wait()
}

func nativeAddressRecord(line string, service *zeroconf.ServiceEntry) *zeroconf.ServiceEntry {
	fields := strings.Fields(line)
	if len(fields) < 7 || fields[1] != "Add" {
		return nil
	}
	host, _, _ := strings.Cut(fields[len(fields)-2], "%")
	address := net.ParseIP(host)
	if address == nil {
		return nil
	}
	entry := *service
	entry.ReceivedIfIndex, _ = strconv.Atoi(fields[len(fields)-4])
	if address.To4() != nil {
		entry.AddrIPv4 = []net.IP{address}
	} else {
		entry.AddrIPv6 = []net.IP{address}
	}
	return &entry
}

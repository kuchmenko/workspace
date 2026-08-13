package syncdiscovery

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/grandcat/zeroconf"
)

const Service = "_ws-sync._tcp"
const Domain = "local."

type Record struct {
	Name        string
	ServiceID   string
	Protocol    int
	Fingerprint string
	Endpoint    string
}

func TXT(record Record) []string {
	return []string{"name=" + record.Name, "id=" + record.ServiceID, "protocol=" + strconv.Itoa(record.Protocol), "fingerprint=" + record.Fingerprint, "endpoint=" + record.Endpoint}
}

func ParseTXT(values []string) (Record, error) {
	fields := map[string]string{}
	for _, value := range values {
		key, data, ok := strings.Cut(value, "=")
		if !ok || fields[key] != "" {
			return Record{}, errors.New("invalid discovery TXT")
		}
		fields[key] = data
	}
	protocol, err := strconv.Atoi(fields["protocol"])
	endpoint, urlErr := url.Parse(fields["endpoint"])
	record := Record{Name: fields["name"], ServiceID: fields["id"], Protocol: protocol, Fingerprint: strings.ToLower(fields["fingerprint"]), Endpoint: fields["endpoint"]}
	if err != nil || urlErr != nil || record.Name == "" || record.ServiceID == "" || protocol <= 0 || len(record.Fingerprint) != 64 || endpoint.Scheme != "https" || endpoint.Host == "" {
		return Record{}, errors.New("invalid discovery TXT")
	}
	for _, char := range record.Fingerprint {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return Record{}, errors.New("invalid discovery fingerprint")
		}
	}
	return record, nil
}

func Deduplicate(records []Record) []Record {
	byKey := map[string]Record{}
	for _, record := range records {
		byKey[record.ServiceID+"\x00"+record.Endpoint] = record
	}
	out := make([]Record, 0, len(byKey))
	for _, record := range byKey {
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		if out[i].Endpoint != out[j].Endpoint {
			return out[i].Endpoint < out[j].Endpoint
		}
		return out[i].ServiceID < out[j].ServiceID
	})
	return out
}

func Register(record Record, port int) (*zeroconf.Server, error) {
	if _, err := ParseTXT(TXT(record)); err != nil {
		return nil, err
	}
	return zeroconf.Register(record.Name, Service, Domain, port, TXT(record), nil)
}

func Browse(ctx context.Context) ([]Record, error) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, err
	}
	entries := make(chan *zeroconf.ServiceEntry)
	var records []Record
	done := make(chan struct{})
	go func() {
		defer close(done)
		for entry := range entries {
			if record, parseErr := ParseTXT(entry.Text); parseErr == nil {
				records = append(records, record)
			}
		}
	}()
	if err = resolver.Browse(ctx, Service, Domain, entries); err != nil {
		close(entries)
		<-done
		return nil, err
	}
	<-ctx.Done()
	<-done
	return Deduplicate(records), nil
}

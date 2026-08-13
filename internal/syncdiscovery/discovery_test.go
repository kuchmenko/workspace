package syncdiscovery

import "testing"

func TestTXTParseValidationAndDedup(t *testing.T) {
	record := Record{Name: "home", ServiceID: "id", Protocol: 1, Fingerprint: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Endpoint: "https://home.local:47321"}
	parsed, err := ParseTXT(TXT(record))
	if err != nil || parsed != record {
		t.Fatalf("parse = %+v, %v", parsed, err)
	}
	for _, bad := range [][]string{{"name=x"}, append(TXT(record), "id=other"), {"name=x", "id=y", "protocol=1", "fingerprint=no", "endpoint=http://x"}} {
		if _, err := ParseTXT(bad); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	got := Deduplicate([]Record{record, record, {Name: "other", ServiceID: "z", Protocol: 1, Fingerprint: record.Fingerprint, Endpoint: "https://z.local:1"}})
	if len(got) != 2 || got[0].Name != "home" {
		t.Fatalf("dedup = %+v", got)
	}
}

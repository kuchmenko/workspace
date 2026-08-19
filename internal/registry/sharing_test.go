package registry

import "testing"

func TestRemoteContainsCredentials(t *testing.T) {
	tests := map[string]bool{
		"https://example.com/repo.git":                     false,
		"https://example.com/repo.git?token=secret":        true,
		"https://example.com/repo.git#access_token=secret": true,
		"https://user@example.com/repo.git":                true,
		"https://user:secret@example.com/%zz":              true,
		"ssh://git@example.com/repo.git":                   false,
		"ssh://git:secret@example.com/repo.git":            true,
		"git@example.com:owner/repo.git":                   false,
		"secret@example.com:owner/repo.git":                true,
		"example.com:owner/repo.git":                       false,
	}
	for remote, expected := range tests {
		if actual := RemoteContainsCredentials(remote); actual != expected {
			t.Errorf("RemoteContainsCredentials(%q) = %t, want %t", remote, actual, expected)
		}
	}
}

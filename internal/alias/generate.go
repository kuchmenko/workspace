package alias

import (
	"strings"
)

// Generate produces a short alias name from a project or group name.
//
// Rules:
//  1. Two parts separated by - or _, each ≤4 chars → join (mm-eh → mmeh).
//  2. Multi-part separated by - or _ → first letter of each (claude-code → cc,
//     my-cool-project → mcp).
//  3. Single word → consonants, max 5 chars (limitless → lmtls).
//
// On collision with existing names in `taken`, a numeric suffix is appended.
func Generate(name string, taken map[string]struct{}) string {
	base := generateBase(name)
	if base == "" {
		base = name
	}
	if _, clash := taken[base]; !clash {
		return base
	}
	for i := 2; i < 1000; i++ {
		cand := base + itoa(i)
		if _, clash := taken[cand]; !clash {
			return cand
		}
	}
	return base
}

func generateBase(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}

	parts := splitParts(name)
	if len(parts) >= 2 {
		// Rule 1: two short parts → join
		if len(parts) == 2 && len(parts[0]) <= 4 && len(parts[1]) <= 4 {
			return parts[0] + parts[1]
		}
		// Rule 2: first letters
		var b strings.Builder
		for _, p := range parts {
			if p == "" {
				continue
			}
			b.WriteByte(p[0])
		}
		return b.String()
	}

	// Rule 3: consonants from single word
	var b strings.Builder
	// always keep first character even if vowel
	b.WriteByte(name[0])
	for i := 1; i < len(name) && b.Len() < 5; i++ {
		c := name[i]
		if !isVowel(c) {
			b.WriteByte(c)
		}
	}
	return b.String()
}

func splitParts(s string) []string {
	var parts []string
	var cur strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' || c == '_' {
			if cur.Len() > 0 {
				parts = append(parts, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteByte(c)
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

func isVowel(c byte) bool {
	switch c {
	case 'a', 'e', 'i', 'o', 'u', 'y':
		return true
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [4]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

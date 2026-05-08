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
//  3. Single word → consonants, max 5 chars (example → lmtls).
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
		return multiPartName(parts)
	}
	return consonantSqueeze(name)
}

// multiPartName applies Rule 1 + Rule 2: two short parts (each ≤4
// chars) join verbatim ("co-op" → "coop"); anything else collapses to
// the per-part first-letter acronym ("api-gateway" → "ag").
func multiPartName(parts []string) string {
	if len(parts) == 2 && len(parts[0]) <= 4 && len(parts[1]) <= 4 {
		return parts[0] + parts[1]
	}
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteByte(p[0])
	}
	return b.String()
}

// consonantSqueeze applies Rule 3 to a single-word name: keep the
// first character (even if vowel) and append up to four more
// consonants, capping output at five chars total.
func consonantSqueeze(name string) string {
	var b strings.Builder
	b.WriteByte(name[0])
	for i := 1; i < len(name) && b.Len() < 5; i++ {
		c := name[i]
		if !isVowel(c) {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// splitParts splits `s` on '-' or '_' separators, dropping empty
// fragments. Equivalent to strings.FieldsFunc with a hand-rolled
// predicate that only treats '-' and '_' as separators.
func splitParts(s string) []string {
	return strings.FieldsFunc(s, isSeparator)
}

func isSeparator(r rune) bool {
	return r == '-' || r == '_'
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

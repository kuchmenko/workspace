package agent

import (
	"strings"
	"unicode/utf8"
)

func presentLabel(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); {
		if value[i] == 0x1b {
			i = skipEscape(value, i+1)
			continue
		}
		r, size := rune(value[i]), 1
		if r >= 0x80 {
			r, size = utf8.DecodeRuneInString(value[i:])
		}
		i += size
		if r < 0x20 || r == 0x7f || r >= 0x80 && r <= 0x9f || unsafeBidi(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func skipEscape(value string, i int) int {
	if i >= len(value) {
		return i
	}
	switch value[i] {
	case '[':
		for i++; i < len(value); i++ {
			if value[i] >= 0x40 && value[i] <= 0x7e {
				return i + 1
			}
		}
	case ']':
		for i++; i < len(value); i++ {
			if value[i] == 0x07 {
				return i + 1
			}
			if value[i] == 0x1b && i+1 < len(value) && value[i+1] == '\\' {
				return i + 2
			}
		}
	default:
		return i + 1
	}
	return len(value)
}

func unsafeBidi(r rune) bool {
	return r == '\u061c' || r == '\u200e' || r == '\u200f' || r >= '\u202a' && r <= '\u202e' || r >= '\u2066' && r <= '\u2069'
}

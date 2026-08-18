package cli

import (
	"fmt"
	"strings"
)

func terminalText(value string) string {
	var escaped strings.Builder
	for _, character := range value {
		if character <= 0x1f || character == 0x7f || character >= 0x80 && character <= 0x9f {
			fmt.Fprintf(&escaped, "\\x%02X", character)
			continue
		}
		escaped.WriteRune(character)
	}
	return escaped.String()
}

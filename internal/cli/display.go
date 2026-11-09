package cli

import (
	"fmt"
	"strings"
	"unicode"
)

// SafeText preserves ordinary layout without executing terminal controls.
func SafeText(text string) string {
	var b strings.Builder
	for _, r := range text {
		if r != '\n' && r != '\t' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
			fmt.Fprintf(&b, "\\u%04x", r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

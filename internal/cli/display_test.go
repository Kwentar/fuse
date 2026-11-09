package cli

import (
	"strings"
	"testing"
)

func TestTerminalControlsAreInert(t *testing.T) {
	out := SafeText("Привет\n\t\x1b[2J\x9b\r\x00\u202e")
	if !strings.HasPrefix(out, "Привет\n\t") || strings.ContainsAny(out, "\x1b\r\x00\u202e") || !strings.Contains(out, `\u001b[2J`) {
		t.Fatal(out)
	}
}

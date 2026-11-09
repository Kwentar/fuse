package messaging

import (
	"strings"
	"testing"
)

func TestValidation(t *testing.T) {
	m, e := New(strings.Repeat("я", 32768))
	if e != nil {
		t.Fatal(e)
	}
	if (Reply{Version: 1, Type: "ack", ID: m.ID, Status: "stored"}).Validate(m.ID) != nil {
		t.Fatal("valid ACK")
	}
	for _, r := range []Reply{{Version: 2, Type: "ack", ID: m.ID, Status: "stored"}, {Version: 1, Type: "ack", ID: m.ID, Status: "read"}, {Version: 1, Type: "ack", ID: "other", Status: "stored"}, {Version: 1, Type: "error", ID: m.ID, Code: "unknown"}} {
		if r.Validate(m.ID) == nil {
			t.Fatal("invalid ACK accepted")
		}
	}
	m.Text += "x"
	if m.Validate() == nil {
		t.Fatal("oversized text")
	}
	m.Text = string([]byte{255})
	if m.Validate() == nil {
		t.Fatal("bad UTF-8")
	}
	m.Text = "ok"
	m.ID = "550e8400-e29b-11d4-a716-446655440000"
	if m.Validate() == nil {
		t.Fatal("non v4 UUID")
	}
}

package wire

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestEscapedMaximumAndBounds(t *testing.T) {
	v := struct {
		Text string `json:"text"`
	}{strings.Repeat("\x00", MaxText)}
	var b bytes.Buffer
	if e := Write(&b, v); e != nil {
		t.Fatal(e)
	}
	var got struct {
		Text string `json:"text"`
	}
	if e := Read(&b, &got); e != nil || got.Text != v.Text {
		t.Fatal(e)
	}
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], MaxFrame+1)
	if Read(bytes.NewReader(h[:]), &got) == nil {
		t.Fatal("oversized length accepted")
	}
	if Decode([]byte{'"', 255, '"'}, new(string)) == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	if Decode([]byte(`{"text":"x"} {}`), &got) == nil {
		t.Fatal("trailing JSON accepted")
	}
}

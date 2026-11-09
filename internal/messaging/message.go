package messaging

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"fuse/internal/wire"
	"github.com/google/uuid"
)

const Protocol = "/p2p-messenger/chat/1.0.0"

type Message struct {
	Version   int    `json:"version"`
	Type      string `json:"type"`
	ID        string `json:"message_id"`
	Timestamp string `json:"timestamp"`
	Text      string `json:"text"`
}
type Reply struct {
	Version int    `json:"version"`
	Type    string `json:"type"`
	ID      string `json:"message_id,omitempty"`
	Status  string `json:"status,omitempty"`
	Code    string `json:"code,omitempty"`
}

func ValidID(s string) bool {
	u, e := uuid.Parse(s)
	return e == nil && u.Version() == 4 && u.Variant() == uuid.RFC4122 && u.String() == s
}
func New(text string) (Message, error) {
	u, e := uuid.NewRandom()
	if e != nil {
		return Message{}, e
	}
	m := Message{1, "message", u.String(), time.Now().UTC().Format(time.RFC3339Nano), text}
	return m, m.Validate()
}
func (m Message) Validate() error {
	if m.Version != 1 {
		return errors.New("UNSUPPORTED_VERSION")
	}
	if m.Type != "message" || !ValidID(m.ID) || !utf8.ValidString(m.Text) || len(m.Text) > wire.MaxText {
		return errors.New("INVALID_MESSAGE")
	}
	if _, e := time.Parse(time.RFC3339Nano, m.Timestamp); e != nil {
		return errors.New("INVALID_MESSAGE")
	}
	return nil
}
func (m Message) Hash() [32]byte { b, _ := json.Marshal(m); return sha256.Sum256(b) }
func (r Reply) Validate(id string) error {
	if r.Version != 1 || r.ID != id {
		return errors.New("PROTOCOL_ERROR")
	}
	if r.Type == "ack" && (r.Status == "stored" || r.Status == "duplicate") && r.Code == "" {
		return nil
	}
	if r.Type == "error" && r.Status == "" {
		switch r.Code {
		case "UNSUPPORTED_VERSION", "INVALID_MESSAGE", "MESSAGE_ID_CONFLICT", "STORAGE_ERROR", "RATE_LIMITED":
			return nil
		}
	}
	return errors.New("PROTOCOL_ERROR")
}

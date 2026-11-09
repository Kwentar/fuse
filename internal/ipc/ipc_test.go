package ipc

import (
	"context"
	"encoding/json"
	"fuse/internal/securefs"
	"path/filepath"
	"testing"
	"time"
)

func TestStreamingResponsesAndIdentity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if e := securefs.EnsureDir(dir); e != nil {
		t.Fatal(e)
	}
	l, e := Listen(dir)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, l, func(ctx context.Context, req Request, emit func(any) error) error {
			for i := 0; i < 100; i++ {
				if e := emit(map[string]int{"row": i}); e != nil {
					return e
				}
			}
			return nil
		})
	}()
	defer func() {
		cancel()
		l.Close()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	deadline, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	rows := 0
	if e = Call(deadline, dir, "history", map[string]int{"limit": 100}, func(data json.RawMessage) error {
		var row map[string]int
		if e := json.Unmarshal(data, &row); e != nil {
			return e
		}
		if row["row"] != rows {
			t.Errorf("out of order response")
		}
		rows++
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if rows != 100 {
		t.Fatal(rows)
	}
}

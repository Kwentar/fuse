package node

import (
	"context"
	"fuse/internal/appinit"
	"fuse/internal/config"
	"fuse/internal/ipc"
	"fuse/internal/messaging"
	"fuse/internal/server"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in desktop UI fixture: all listening sockets belong to the stable test binary.
func TestDesktopUIFixture(t *testing.T) {
	base := os.Getenv("P2PM_UI_FIXTURE")
	if base == "" {
		t.Skip("UI fixture opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	sc := config.DefaultServer()
	sc.DataDir = filepath.Join(base, "server")
	sc.Listen = []string{"/ip4/127.0.0.1/tcp/0"}
	sc.AllowPrivate = true
	server, e := server.Open(ctx, sc)
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	c := config.DefaultClient()
	c.AllowPrivate = true
	c.SmartRouting = true
	c.Direct = []string{"/ip4/0.0.0.0/tcp/0"}
	c.PublishInterval = "2s"
	c.Bootstrap = []string{server.Host.Addrs()[0].String() + "/p2p/" + server.Host.ID().String()}
	c.Relays = c.Bootstrap
	aDir, bDir := filepath.Join(base, "profile"), filepath.Join(base, "other")
	appinit.Init(aDir, c)
	appinit.Init(bDir, c)
	a, e := Open(ctx, aDir, c)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	b, e := Open(ctx, bDir, c)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	a.AddContact(b.Host.ID(), "Тестовый ноутбук")
	b.AddContact(a.Host.ID(), "Windows · тест")
	a.SetDownloadDir(filepath.Join(base, "Downloads"))
	m, _ := messaging.New("Привет! Здесь можно отправить сразу всю папку с фото и видео.")
	a.Journal.Receive(b.Host.ID(), m)
	m, _ = messaging.New("Файлы сохраняются автоматически. Просто перетащи их в окно чата.")
	a.Journal.Receive(b.Host.ID(), m)
	source := filepath.Join(base, "Отпуск — пример.jpg")
	os.WriteFile(source, make([]byte, 1000000), 0600)
	b.SendFile(ctx, a.Host.ID(), source)
	listener, e := ipc.Listen(aDir)
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	t.Log("UI fixture ready", aDir)
	go ipc.Serve(ctx, listener, a.Control)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
			if _, e := os.Stat(filepath.Join(base, "stop")); e == nil {
				return
			}
		}
	}
}

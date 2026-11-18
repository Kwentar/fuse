package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"

	"fuse/internal/appinit"
	"fuse/internal/config"
	"fuse/internal/discovery"
	"fuse/internal/identity"
	"fuse/internal/ipc"
	"fuse/internal/messaging"
	"fuse/internal/node"
	"fuse/internal/securefs"
	"fuse/internal/storage"
	"fuse/internal/wire"
	"github.com/libp2p/go-libp2p/core/peer"
)

const Version = "0.4.1"

func Main(args []string) int {
	code, err := run(args)
	if err != nil {
		b, _ := json.Marshal(map[string]string{"error": err.Error()})
		fmt.Fprintln(os.Stderr, string(b))
		if code == 0 {
			code = errorCode(err)
		}
	}
	return code
}
func errorCode(e error) int {
	s := e.Error()
	switch {
	case strings.Contains(s, "IPC_RESULT_UNKNOWN"):
		return 4
	case strings.HasPrefix(s, "INVALID_") || strings.HasPrefix(s, "USAGE"):
		return 2
	case strings.Contains(s, "TIMEOUT") || strings.Contains(s, "CONTACT_NOT_FOUND") || strings.Contains(s, "NODE_NOT_RUNNING") || strings.Contains(s, "NO_USABLE_ADDRESS"):
		return 3
	default:
		return 6
	}
}
func run(args []string) (int, error) {
	dir := ""
	machine := false
	limit := 50
	stdin := false
	gui := false
	pos := []string{}
	for i := 0; i < len(args); i++ {
		s := args[i]
		switch s {
		case "--":
			pos = append(pos, args[i+1:]...)
			i = len(args)
		case "--data-dir":
			i++
			if i >= len(args) {
				return 2, errors.New("USAGE: --data-dir requires a path")
			}
			dir = args[i]
		case "--json":
			machine = true
		case "--limit":
			i++
			if i >= len(args) {
				return 2, errors.New("INVALID_LIMIT")
			}
			var e error
			limit, e = strconv.Atoi(args[i])
			if e != nil || limit <= 0 {
				return 2, errors.New("INVALID_LIMIT")
			}
		case "--stdin":
			stdin = true
		case "--gui":
			gui = true
		default:
			pos = append(pos, s)
		}
	}
	if len(pos) == 0 || pos[0] == "help" || pos[0] == "--help" {
		fmt.Println("messenger [--data-dir PATH] [--json] init|id|run|status|find PEER|send PEER TEXT|retry ID|history [PEER] [--limit N]|peers|version\nsend PEER --stdin reads up to 64 KiB UTF-8; run must stay open.")
		return 0, nil
	}
	if pos[0] == "version" {
		build, _ := debug.ReadBuildInfo()
		fmt.Printf("{\"version\":%q,\"go\":%q,\"os\":%q,\"arch\":%q,\"build\":%q}\n", Version, runtime.Version(), runtime.GOOS, runtime.GOARCH, build.String())
		return 0, nil
	}
	if dir == "" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			var e error
			base, e = os.UserConfigDir()
			if e != nil {
				return 6, e
			}
		}
		dir = filepath.Join(base, "P2PMessenger")
	}
	var e error
	dir, e = filepath.Abs(dir)
	if e != nil {
		return 6, e
	}
	ctx, cancel := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer cancel()
	command := pos[0]
	if command == "init" {
		if len(pos) != 1 {
			return 2, errors.New("USAGE: init")
		}
		id, e := appinit.Init(dir, config.WithDefaultNetwork(config.DefaultClient()))
		if e != nil {
			return 6, e
		}
		if machine {
			fmt.Printf("{\"peer_id\":%q}\n", id.String())
		} else {
			fmt.Println(id)
		}
		return 0, nil
	}
	if command == "run" {
		if len(pos) != 1 {
			return 2, errors.New("USAGE: run")
		}
		c, e := config.LoadClient(filepath.Join(dir, "config.json"))
		if e != nil {
			return 6, fmt.Errorf("NODE_NOT_READY: run init and configure the data directory: %w", e)
		}
		if gui {
			if e = prepareGUIProfile(dir); e != nil {
				return 6, e
			}
			c.SmartRouting = true
		}
		n, e := node.Open(ctx, dir, c, func(p peer.ID, m messaging.Message) {
			if !machine {
				fmt.Printf("\n[%s] %s\n%s\n", p.String(), m.ID, SafeText(m.Text))
				return
			}
			b, _ := json.Marshal(map[string]any{"type": "received", "peer_id": p.String(), "message_id": m.ID, "timestamp": m.Timestamp, "text": m.Text})
			fmt.Println(string(b))
		})
		if e != nil {
			return 6, e
		}
		defer n.Close()
		listener, e := ipc.Listen(dir)
		if e != nil {
			return 6, e
		}
		defer listener.Close()
		b, _ := json.Marshal(n.Status())
		fmt.Println(string(b))
		return 0, ipc.Serve(ctx, listener, n.Control)
	}
	p := node.Params{Limit: limit}
	exit := 0
	switch command {
	case "queue-file":
		if len(pos) != 4 {
			return 2, errors.New("USAGE: queue-file PEER PATH FILE_ID")
		}
		p.Peer = pos[1]
		p.Path = pos[2]
		p.ID = pos[3]
	case "publish-file":
		if len(pos) != 3 {
			return 2, errors.New("USAGE: publish-file FILE_ID CONTENT_URI")
		}
		p.ID = pos[1]
		p.Path = pos[2]
	case "pause-files", "resume-files":
		if len(pos) != 1 || !stdin {
			return 2, errors.New("USAGE: pause-files|resume-files --stdin")
		}
		b, e := io.ReadAll(io.LimitReader(os.Stdin, 256<<10))
		if e != nil {
			return 2, e
		}
		if e = wire.Decode(b, &p.IDs); e != nil {
			return 2, e
		}
	case "download-dir":
		if len(pos) > 2 {
			return 2, errors.New("USAGE: download-dir [PATH]")
		}
		if len(pos) == 2 {
			p.Path = pos[1]
		}
	case "send-files":
		if len(pos) != 2 || !stdin {
			return 2, errors.New("USAGE: send-files PEER --stdin (JSON array of paths)")
		}
		p.Peer = pos[1]
		b, e := io.ReadAll(io.LimitReader(os.Stdin, 256<<10))
		if e != nil {
			return 2, e
		}
		if e = wire.Decode(b, &p.Paths); e != nil {
			return 2, e
		}
	case "pause-file", "resume-file":
		if len(pos) != 2 || !messaging.ValidID(pos[1]) {
			return 2, errors.New("INVALID_FILE_ID")
		}
		p.ID = pos[1]
	case "contacts":
		if len(pos) != 1 {
			return 2, errors.New("USAGE: contacts")
		}
	case "add-contact":
		if len(pos) != 3 {
			return 2, errors.New("USAGE: add-contact PEER NAME")
		}
		p.Peer = pos[1]
		p.Name = pos[2]
	case "files":
		if len(pos) > 2 {
			return 2, errors.New("USAGE: files [PEER]")
		}
		if len(pos) == 2 {
			p.Peer = pos[1]
		}
	case "send-file":
		if len(pos) != 3 {
			return 2, errors.New("USAGE: send-file PEER PATH")
		}
		p.Peer = pos[1]
		p.Path = pos[2]
	case "id", "status", "peers":
		if len(pos) != 1 {
			return 2, errors.New("USAGE: unexpected arguments")
		}
	case "find":
		if len(pos) != 2 {
			return 2, errors.New("USAGE: find PEER_ID")
		}
		id, e := discovery.ParseID(pos[1])
		if e != nil {
			return 2, e
		}
		p.Peer = id.String()
	case "send":
		if len(pos) < 2 {
			return 2, errors.New("USAGE: send PEER_ID TEXT or --stdin")
		}
		id, e := discovery.ParseID(pos[1])
		if e != nil {
			return 2, e
		}
		p.Peer = id.String()
		text := strings.Join(pos[2:], " ")
		if stdin {
			if len(pos) != 2 {
				return 2, errors.New("USAGE: use text or --stdin")
			}
			b, e := io.ReadAll(io.LimitReader(os.Stdin, wire.MaxText+1))
			if e != nil {
				return 2, e
			}
			text = string(b)
		} else if len(pos) < 3 {
			return 2, errors.New("USAGE: message text required")
		}
		m, e := messaging.New(text)
		if e != nil {
			return 2, e
		}
		p.Message = &m
	case "retry":
		if len(pos) != 2 || !messaging.ValidID(pos[1]) {
			return 2, errors.New("INVALID_MESSAGE_ID")
		}
		p.ID = pos[1]
	case "history":
		if len(pos) > 2 {
			return 2, errors.New("USAGE: history [PEER_ID]")
		}
		if len(pos) == 2 {
			id, e := discovery.ParseID(pos[1])
			if e != nil {
				return 2, e
			}
			p.Peer = id.String()
		}
	default:
		return 2, errors.New("USAGE: unknown command; use help")
	}
	emit := func(b json.RawMessage) error {
		if command == "send" || command == "retry" {
			var r node.Delivery
			if e := json.Unmarshal(b, &r); e != nil {
				return e
			}
			switch r.Status {
			case "not_sent":
				exit = 3
			case "delivery_unknown":
				exit = 4
			case "rejected":
				exit = 5
			}
		}
		if command == "history" && !machine {
			var event storage.Event
			if e := json.Unmarshal(b, &event); e != nil {
				return e
			}
			fmt.Printf("%s %s %s [%s] %s\n%s\n", event.LocalTime, event.Direction, event.Peer, event.Status, event.ID, SafeText(event.Message.Text))
		} else if command == "id" && !machine {
			var v struct {
				Peer string `json:"peer_id"`
			}
			if e := json.Unmarshal(b, &v); e != nil {
				return e
			}
			fmt.Println(v.Peer)
		} else {
			fmt.Println(string(b))
		}
		return nil
	}
	e = ipc.Call(ctx, dir, command, p, emit)
	if e != nil && e.Error() == "NODE_NOT_RUNNING" && (command == "id" || command == "history") {
		if e = securefs.CheckFiles(dir, "identity.key", "state.json", "config.json", "peer-cache.json", "messages.jsonl"); e != nil {
			return 6, e
		}
		lock, e := securefs.Acquire(dir)
		if e != nil {
			return 6, e
		}
		defer lock.Close()
		if command == "id" {
			key, e := identity.Load(dir)
			if e != nil {
				return 6, e
			}
			id, e := peer.IDFromPrivateKey(key)
			if e != nil {
				return 6, e
			}
			b, _ := json.Marshal(map[string]string{"peer_id": id.String()})
			return 0, emit(b)
		}
		journal, e := storage.Open(dir)
		if e != nil {
			return 6, e
		}
		defer journal.Close()
		return 0, journal.History(p.Peer, p.Limit, func(event storage.Event) error {
			b, e := json.Marshal(event)
			if e != nil {
				return e
			}
			return emit(b)
		})
	}
	if e != nil && strings.Contains(e.Error(), "IPC_RESULT_UNKNOWN") && p.Message != nil {
		fmt.Printf("{\"message_id\":%q,\"status\":\"delivery_unknown\",\"code\":\"IPC_RESULT_UNKNOWN\"}\n", p.Message.ID)
	}
	if e != nil {
		return errorCode(e), e
	}
	return exit, nil
}

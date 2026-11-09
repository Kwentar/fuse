package node

import (
	"context"
	"errors"
	"fuse/internal/discovery"
	"fuse/internal/ipc"
	"fuse/internal/messaging"
	"fuse/internal/storage"
	"fuse/internal/wire"
)

type Params struct {
	Name    string             `json:"name,omitempty"`
	Path    string             `json:"path,omitempty"`
	Paths   []string           `json:"paths,omitempty"`
	IDs     []string           `json:"file_ids,omitempty"`
	Peer    string             `json:"peer_id,omitempty"`
	Message *messaging.Message `json:"message,omitempty"`
	ID      string             `json:"message_id,omitempty"`
	Limit   int                `json:"limit,omitempty"`
}

func (n *Node) Control(ctx context.Context, req ipc.Request, emit func(any) error) error {
	var p Params
	if e := wire.Decode(req.Params, &p); e != nil {
		return errors.New("INVALID_REQUEST")
	}
	switch req.Method {
	case "queue-file":
		id, e := discovery.ParseID(p.Peer)
		if e != nil {
			return e
		}
		r, e := n.QueueFile(id, p.Path, p.ID)
		if e != nil {
			return e
		}
		return emit(r)
	case "publish-file":
		if e := n.PublishFile(p.ID, p.Path); e != nil {
			return e
		}
		return emit(map[string]string{"status": "saved"})
	case "pause-files", "resume-files":
		if len(p.IDs) == 0 || len(p.IDs) > 128 {
			return errors.New("INVALID_BATCH")
		}
		action := "pause"
		if req.Method == "resume-files" {
			action = "resume"
		}
		for _, id := range p.IDs {
			r := map[string]string{"file_id": id, "status": action}
			if e := n.FileAction(id, action); e != nil {
				r["error"] = e.Error()
			}
			if e := emit(r); e != nil {
				return e
			}
		}
		return nil
	case "download-dir":
		if p.Path != "" {
			if e := n.SetDownloadDir(p.Path); e != nil {
				return e
			}
		}
		return emit(map[string]string{"path": n.DownloadDir()})
	case "send-files":
		id, e := discovery.ParseID(p.Peer)
		if e != nil {
			return e
		}
		if len(p.Paths) == 0 || len(p.Paths) > 128 {
			return errors.New("INVALID_BATCH: 1..128 files per request")
		}
		for _, path := range p.Paths {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			r, e := n.SendFile(ctx, id, path)
			if e != nil {
				r.Path = path
				r.Status = "failed"
				r.Error = e.Error()
			}
			if e = emit(r); e != nil {
				return e
			}
		}
		return nil
	case "pause-file", "resume-file":
		action := "resume"
		if req.Method == "pause-file" {
			action = "pause"
		}
		if e := n.FileAction(p.ID, action); e != nil {
			return e
		}
		return emit(map[string]string{"status": action})
	case "contacts":
		c, e := n.Contacts()
		if e != nil {
			return e
		}
		for _, v := range c {
			if e = emit(v); e != nil {
				return e
			}
		}
		return nil
	case "add-contact":
		id, e := discovery.ParseID(p.Peer)
		if e != nil {
			return e
		}
		if e = n.AddContact(id, p.Name); e != nil {
			return e
		}
		return emit(map[string]string{"status": "saved"})
	case "files":
		rows, e := n.Files(p.Peer)
		if e != nil {
			return e
		}
		for _, r := range rows {
			if e = emit(r); e != nil {
				return e
			}
		}
		return nil
	case "send-file":
		id, e := discovery.ParseID(p.Peer)
		if e != nil {
			return e
		}
		r, e := n.SendFile(ctx, id, p.Path)
		if r.ID != "" {
			if err := emit(r); err != nil {
				return err
			}
		}
		return e
	case "id":
		return emit(map[string]string{"peer_id": n.Host.ID().String()})
	case "status":
		return emit(n.Status())
	case "peers":
		for _, p := range n.Peers() {
			if e := emit(p); e != nil {
				return e
			}
		}
		return nil
	case "find":
		id, e := discovery.ParseID(p.Peer)
		if e != nil {
			return e
		}
		found, e := n.Find(ctx, id, true)
		if e != nil {
			return e
		}
		return emit(found)
	case "send":
		id, e := discovery.ParseID(p.Peer)
		if e != nil {
			return e
		}
		if p.Message == nil {
			return errors.New("INVALID_MESSAGE")
		}
		r, e := n.Send(ctx, id, *p.Message, false)
		if err := emit(r); err != nil {
			return err
		}
		return e
	case "retry":
		if !messaging.ValidID(p.ID) {
			return errors.New("INVALID_MESSAGE")
		}
		r, e := n.Retry(ctx, p.ID)
		if err := emit(r); err != nil {
			return err
		}
		return e
	case "history":
		if p.Limit <= 0 {
			return errors.New("INVALID_LIMIT")
		}
		if p.Peer != "" {
			id, e := discovery.ParseID(p.Peer)
			if e != nil {
				return e
			}
			p.Peer = id.String()
		}
		return n.Journal.History(p.Peer, p.Limit, func(e storage.Event) error { return emit(e) })
	default:
		return errors.New("INVALID_METHOD")
	}
}

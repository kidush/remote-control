package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kidush/remote-control/internal/ac"
)

// Request is one line a client sends.
type Request struct {
	Op     string     `json:"op"` // status | watch | change | preset | resend | fetch
	Device string     `json:"device,omitempty"`
	Change *ac.Change `json:"change,omitempty"`
	Preset string     `json:"preset,omitempty"`
}

// Response is one line the daemon sends back. watch sends one per change.
type Response struct {
	OK      bool       `json:"ok"`
	Error   string     `json:"error,omitempty"`
	Devices []Snapshot `json:"devices"`
}

type Daemon struct {
	units []*Unit
	store *Store

	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func New(store *Store) *Daemon {
	return &Daemon{store: store, subs: map[chan struct{}]struct{}{}}
}

// Add registers a unit. Its onChange must call d.Changed.
func (d *Daemon) Add(u *Unit) { d.units = append(d.units, u) }

func (d *Daemon) unit(id string) (*Unit, error) {
	for _, u := range d.units {
		if u.cfg.ID == id {
			return u, nil
		}
	}
	return nil, fmt.Errorf("unknown device %q", id)
}

func (d *Daemon) snapshots() []Snapshot {
	out := make([]Snapshot, 0, len(d.units))
	for _, u := range d.units {
		out = append(out, u.Snapshot())
	}
	return out
}

// Changed saves confirmed states and wakes every watcher.
func (d *Daemon) Changed() {
	states := map[string]ac.State{}
	for _, u := range d.units {
		states[u.cfg.ID] = u.Confirmed()
	}
	if d.store != nil {
		if err := d.store.Save(states); err != nil {
			log.Printf("save state: %v", err)
		}
	}
	d.mu.Lock()
	for ch := range d.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	d.mu.Unlock()
}

func (d *Daemon) subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	d.mu.Lock()
	d.subs[ch] = struct{}{}
	d.mu.Unlock()
	return ch
}

func (d *Daemon) unsubscribe(ch chan struct{}) {
	d.mu.Lock()
	delete(d.subs, ch)
	d.mu.Unlock()
}

// Listen opens the socket, replacing a stale one left by a crash but refusing
// to start when another daemon is still answering on it.
func Listen(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
		conn.Close()
		return nil, fmt.Errorf("a daemon is already running on %s", path)
	}
	_ = os.Remove(path)
	return net.Listen("unix", path)
}

func (d *Daemon) Serve(ctx context.Context, ln net.Listener) error {
	for _, u := range d.units {
		go u.Run(ctx)
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			log.Printf("accept: %v", err)
			continue
		}
		go d.handle(ctx, conn)
	}
}

func (d *Daemon) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	enc := json.NewEncoder(conn)
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		enc.Encode(Response{Error: "bad request: " + err.Error()})
		return
	}
	if req.Op == "watch" {
		d.watch(ctx, conn, enc)
		return
	}
	err = d.do(ctx, req)
	resp := Response{OK: err == nil, Devices: d.snapshots()}
	if err != nil {
		resp.Error = err.Error()
	}
	enc.Encode(resp)
}

func (d *Daemon) do(ctx context.Context, req Request) error {
	if req.Op == "status" {
		return nil
	}
	u, err := d.unit(req.Device)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout+5*time.Second)
	defer cancel()
	switch req.Op {
	case "change":
		if req.Change == nil || req.Change.IsZero() {
			return errors.New("empty change")
		}
		return u.Change(ctx, *req.Change)
	case "preset":
		for _, p := range u.cfg.Presets {
			if p.Name == req.Preset {
				return u.Change(ctx, p.Change())
			}
		}
		return fmt.Errorf("unknown preset %q", req.Preset)
	case "resend":
		return u.Resend(ctx)
	case "fetch":
		return u.Fetch(ctx)
	}
	return fmt.Errorf("unknown op %q", req.Op)
}

// watch streams a snapshot now and after every change until the client goes
// away. A reader goroutine notices the hangup.
func (d *Daemon) watch(ctx context.Context, conn net.Conn, enc *json.Encoder) {
	ch := d.subscribe()
	defer d.unsubscribe(ch)
	gone := make(chan struct{})
	go func() {
		buf := make([]byte, 64)
		for {
			if _, err := conn.Read(buf); err != nil {
				close(gone)
				return
			}
		}
	}()
	for {
		if err := enc.Encode(Response{OK: true, Devices: d.snapshots()}); err != nil {
			return
		}
		select {
		case <-ch:
		case <-gone:
			return
		case <-ctx.Done():
			return
		}
	}
}

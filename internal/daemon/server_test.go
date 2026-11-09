package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kidush/remote-control/internal/ac"
	"github.com/kidush/remote-control/internal/config"
	"github.com/kidush/remote-control/internal/device"
)

func TestSocketRoundTripAndPersistence(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "rc.sock")
	store := &Store{Path: filepath.Join(dir, "state.json")}
	fake := &device.Fake{}

	d := New(store)
	d.Add(NewUnit(config.Default().Devices[0], fake, "", ac.Default(), d.Changed))
	ln, err := Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Serve(ctx, ln)

	if _, err := Listen(socket); err == nil {
		t.Fatal("a second daemon started on a live socket")
	}

	// A watcher sees the initial state and then the change.
	seen := make(chan ac.State, 10)
	go Watch(socket, func(r Response) error {
		seen <- r.Devices[0].Confirmed
		return nil
	})
	if s := <-seen; s.Power {
		t.Fatalf("initial state %+v", s)
	}

	resp, err := Do(socket, Request{Op: "preset", Device: "ac", Preset: "Quick cool"})
	if err != nil {
		t.Fatal(err)
	}
	want := ac.State{Power: true, Mode: ac.Cool, Temp: 18, Fan: ac.SpeedHigh}
	if got := resp.Devices[0].Confirmed; got != want {
		t.Fatalf("confirmed %+v, want %+v", got, want)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case s := <-seen:
			if s == want {
				goto watched
			}
		case <-deadline:
			t.Fatal("watcher never saw the change")
		}
	}
watched:
	saved, err := store.Load()
	if err != nil || saved["ac"] != want {
		t.Fatalf("saved %+v, %v", saved, err)
	}

	if _, err := Do(socket, Request{Op: "change", Device: "nope", Change: &ac.Change{TogglePower: true}}); err == nil {
		t.Fatal("unknown device accepted")
	}
	if _, err := Do(socket, Request{Op: "preset", Device: "ac", Preset: "nope"}); err == nil {
		t.Fatal("unknown preset accepted")
	}
}

func TestClientReportsDaemonDown(t *testing.T) {
	_, err := Do(filepath.Join(t.TempDir(), "missing.sock"), Request{Op: "status"})
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v", err)
	}
}

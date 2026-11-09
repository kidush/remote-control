package daemon

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kidush/remote-control/internal/ac"
	"github.com/kidush/remote-control/internal/config"
	"github.com/kidush/remote-control/internal/device"
)

// gate is a device whose sends block until released, so tests can stack
// changes behind an in-flight send.
type gate struct {
	mu      sync.Mutex
	sent    []ac.State
	release chan struct{}
	fail    atomic.Bool
}

func (g *gate) Capabilities() device.Capabilities       { return device.Capabilities{Swing: true} }
func (g *gate) Fetch(context.Context) (ac.State, error) { return ac.State{}, device.ErrUnsupported }
func (g *gate) Send(ctx context.Context, _, next ac.State) error {
	select {
	case <-g.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	if g.fail.Load() {
		return errors.New("blaster offline")
	}
	g.mu.Lock()
	g.sent = append(g.sent, next)
	g.mu.Unlock()
	return nil
}

func (g *gate) Sent() []ac.State {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]ac.State(nil), g.sent...)
}

func newTestUnit(t *testing.T, dev device.AC, start ac.State) *Unit {
	t.Helper()
	cfg := config.Default().Devices[0]
	cfg.MinTemp, cfg.MaxTemp = 16, 30
	u := NewUnit(cfg, dev, "", start, func() {})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go u.Run(ctx)
	return u
}

func on() ac.State { return ac.State{Power: true, Mode: ac.Cool, Temp: 24, Fan: ac.SpeedAuto} }

func waitPending(t *testing.T, u *Unit) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if u.Snapshot().Pending {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("change never became pending")
}

func TestQuickChangesCoalesceInOrder(t *testing.T) {
	g := &gate{release: make(chan struct{})}
	u := newTestUnit(t, g, on())
	ctx := context.Background()

	errs := make(chan error, 3)
	go func() { errs <- u.Change(ctx, ac.Change{TempDelta: 1}) }()
	waitPending(t, u)
	// The first send (25) is in flight; two more presses queue behind it.
	time.Sleep(20 * time.Millisecond)
	go func() { errs <- u.Change(ctx, ac.Change{TempDelta: 1}) }()
	go func() { errs <- u.Change(ctx, ac.Change{TempDelta: 1}) }()
	for u.Snapshot().State.Temp != 27 {
		time.Sleep(5 * time.Millisecond)
	}
	if s := u.Snapshot(); s.Confirmed.Temp != 24 || !s.Pending {
		t.Fatalf("before sends: confirmed %d pending %v", s.Confirmed.Temp, s.Pending)
	}
	g.release <- struct{}{} // 25
	g.release <- struct{}{} // 27, both queued presses in one frame
	for i := 0; i < 3; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	sent := g.Sent()
	if len(sent) != 2 || sent[0].Temp != 25 || sent[1].Temp != 27 {
		t.Fatalf("sent %+v, want 25 then 27", sent)
	}
	if s := u.Snapshot(); s.Confirmed.Temp != 27 || s.Pending {
		t.Fatalf("after: %+v", s)
	}
}

func TestFailureRevertsAndKeepsError(t *testing.T) {
	g := &gate{release: make(chan struct{}, 1)}
	g.fail.Store(true)
	u := newTestUnit(t, g, on())
	g.release <- struct{}{}
	err := u.Change(context.Background(), ac.Change{TempDelta: -2})
	if err == nil {
		t.Fatal("want error")
	}
	s := u.Snapshot()
	if s.State.Temp != 24 || s.Confirmed.Temp != 24 || s.Pending || s.Error == "" {
		t.Fatalf("after failure: %+v", s)
	}
	// The next good send clears the error.
	g.fail.Store(false)
	g.release <- struct{}{}
	if err := u.Change(context.Background(), ac.Change{TempDelta: -2}); err != nil {
		t.Fatal(err)
	}
	if s := u.Snapshot(); s.Error != "" || s.Confirmed.Temp != 22 {
		t.Fatalf("after recovery: %+v", s)
	}
}

func TestChangesWhileOffAreStoredNotSent(t *testing.T) {
	g := &gate{release: make(chan struct{}, 1)}
	off := on()
	off.Power = false
	u := newTestUnit(t, g, off)
	if err := u.Change(context.Background(), ac.Change{TempDelta: 2}); err != nil {
		t.Fatal(err)
	}
	if len(g.Sent()) != 0 {
		t.Fatal("sent a frame while the unit was off")
	}
	if s := u.Snapshot(); s.Confirmed.Temp != 26 {
		t.Fatalf("temp not kept: %+v", s)
	}
	g.release <- struct{}{}
	if err := u.Change(context.Background(), ac.Change{TogglePower: true}); err != nil {
		t.Fatal(err)
	}
	if sent := g.Sent(); len(sent) != 1 || !sent[0].Power || sent[0].Temp != 26 {
		t.Fatalf("power on sent %+v", sent)
	}
}

func TestExplicitPowerAlwaysSends(t *testing.T) {
	g := &gate{release: make(chan struct{}, 2)}
	off := on()
	off.Power = false
	u := newTestUnit(t, g, off)
	no, yes := false, true
	g.release <- struct{}{}
	if err := u.Change(context.Background(), ac.Change{Power: &no}); err != nil {
		t.Fatal(err)
	}
	g.release <- struct{}{}
	u2 := newTestUnit(t, g, on())
	if err := u2.Change(context.Background(), ac.Change{Power: &yes}); err != nil {
		t.Fatal(err)
	}
	if sent := g.Sent(); len(sent) != 2 || sent[0].Power || !sent[1].Power {
		t.Fatalf("sent %+v, want an off frame then an on frame", sent)
	}
}

func TestNoOpChangeSendsNothing(t *testing.T) {
	g := &gate{release: make(chan struct{})}
	start := on()
	start.Temp = 30
	u := newTestUnit(t, g, start)
	if err := u.Change(context.Background(), ac.Change{TempDelta: 1}); err != nil {
		t.Fatal(err)
	}
	if u.Snapshot().Pending {
		t.Fatal("a no-op change queued a send")
	}
}

func TestUnconfiguredUnitReportsSetup(t *testing.T) {
	cfg := config.Default().Devices[0]
	u := NewUnit(cfg, nil, "fill access_id", ac.Default(), func() {})
	err := u.Change(context.Background(), ac.Change{TogglePower: true})
	if err == nil || err.Error() != "setup needed: fill access_id" {
		t.Fatalf("err = %v", err)
	}
	if u.Snapshot().Setup == "" {
		t.Fatal("snapshot hides the setup message")
	}
}

func TestSwingRejectedWithoutCapability(t *testing.T) {
	u := newTestUnit(t, &device.Cloud{}, on())
	if err := u.Change(context.Background(), ac.Change{ToggleSwing: true}); err == nil {
		t.Fatal("want an error for swing on a backend without it")
	}
}

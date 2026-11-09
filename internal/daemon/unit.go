// Package daemon owns device state, serializes sends to each device, and
// serves the CLI and the bar widget over a Unix socket.
package daemon

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/kidush/remote-control/internal/ac"
	"github.com/kidush/remote-control/internal/config"
	"github.com/kidush/remote-control/internal/device"
)

const sendTimeout = 15 * time.Second

// Snapshot is what clients see of one device.
type Snapshot struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Setup is non-empty when the device cannot be used yet, and says why.
	Setup string `json:"setup,omitempty"`
	// State is what the user asked for, shown right away; Confirmed is what
	// the device last accepted. They differ while Pending.
	State        ac.State            `json:"state"`
	Confirmed    ac.State            `json:"confirmed"`
	Pending      bool                `json:"pending"`
	Error        string              `json:"error,omitempty"`
	Capabilities device.Capabilities `json:"capabilities"`
	Limits       ac.Limits           `json:"limits"`
	Presets      []ac.Preset         `json:"presets"`
}

type waiter struct {
	gen uint64
	ch  chan error
}

// Unit is one AC. Changes update the desired state at once and queue a send;
// one worker sends the newest desired state, so quick repeated changes
// coalesce into fewer frames and never arrive out of order.
type Unit struct {
	cfg      config.Device
	dev      device.AC // nil until set up
	setup    string
	onChange func()

	mu        sync.Mutex
	confirmed ac.State
	desired   ac.State
	gen       uint64 // bumped by every change that needs a send
	doneGen   uint64 // last gen the worker finished
	waiters   []waiter
	lastErr   string
	kick      chan struct{}
}

func NewUnit(cfg config.Device, dev device.AC, setup string, saved ac.State, onChange func()) *Unit {
	st := saved.Normalize(cfg.Limits())
	return &Unit{cfg: cfg, dev: dev, setup: setup, onChange: onChange, confirmed: st, desired: st, kick: make(chan struct{}, 1)}
}

func (u *Unit) Snapshot() Snapshot {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := Snapshot{
		ID: u.cfg.ID, Name: u.cfg.Name, Setup: u.setup,
		State: u.desired, Confirmed: u.confirmed, Pending: u.gen != u.doneGen,
		Error: u.lastErr, Limits: u.cfg.Limits(), Presets: u.cfg.Presets,
	}
	if s.Presets == nil {
		s.Presets = []ac.Preset{}
	}
	if u.dev != nil {
		s.Capabilities = u.dev.Capabilities()
	}
	return s
}

func (u *Unit) Confirmed() ac.State {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.confirmed
}

// Change applies c and waits until the device accepted it (or a later change
// that includes it).
func (u *Unit) Change(ctx context.Context, c ac.Change) error {
	if u.dev == nil {
		return errors.New("setup needed: " + u.setup)
	}
	if !u.dev.Capabilities().Swing && (c.Swing != nil || c.ToggleSwing) {
		c.Swing, c.ToggleSwing = nil, false
		if c.IsZero() {
			return errors.New("this backend cannot control swing")
		}
	}
	u.mu.Lock()
	next, err := u.desired.Apply(c, u.cfg.Limits())
	if err != nil {
		u.mu.Unlock()
		return err
	}
	// An explicit on/off (or a preset) always transmits: the stored state may
	// be stale after Alexa, the phone or the physical remote changed the unit.
	idle := u.gen == u.doneGen && c.Power == nil
	switch {
	case idle && next == u.desired:
		// Nothing changed, e.g. temp up at the maximum.
		u.mu.Unlock()
		return nil
	case idle && !u.confirmed.Power && !next.Power:
		// A remote does not transmit while the unit is off; keep the
		// settings for the next power on.
		u.desired, u.confirmed, u.lastErr = next, next, ""
		u.mu.Unlock()
		u.onChange()
		return nil
	}
	u.desired = next
	return u.enqueueLocked(ctx)
}

// Resend sends the current state again, to bring the unit back in line after
// someone used the physical remote.
func (u *Unit) Resend(ctx context.Context) error {
	if u.dev == nil {
		return errors.New("setup needed: " + u.setup)
	}
	u.mu.Lock()
	return u.enqueueLocked(ctx)
}

// enqueueLocked is called with u.mu held and releases it.
func (u *Unit) enqueueLocked(ctx context.Context) error {
	u.gen++
	w := waiter{gen: u.gen, ch: make(chan error, 1)}
	u.waiters = append(u.waiters, w)
	u.mu.Unlock()
	u.onChange()
	select {
	case u.kick <- struct{}{}:
	default:
	}
	select {
	case err := <-w.ch:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Fetch replaces the state with what the backend reports, when it can and no
// send is in flight.
func (u *Unit) Fetch(ctx context.Context) error {
	if u.dev == nil {
		return errors.New("setup needed: " + u.setup)
	}
	if !u.dev.Capabilities().Fetch {
		return device.ErrUnsupported
	}
	st, err := u.dev.Fetch(ctx)
	if err != nil {
		return err
	}
	u.mu.Lock()
	if u.gen != u.doneGen {
		u.mu.Unlock()
		return errors.New("a command is in flight; try again")
	}
	st.Swing = u.confirmed.Swing
	st = st.Normalize(u.cfg.Limits())
	u.confirmed, u.desired = st, st
	u.mu.Unlock()
	u.onChange()
	return nil
}

// Run is the send worker. It returns when ctx ends.
func (u *Unit) Run(ctx context.Context) {
	if u.dev == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-u.kick:
		}
		for u.sendOnce(ctx) {
		}
	}
}

// sendOnce sends the newest desired state if one is queued. It reports
// whether it did any work.
func (u *Unit) sendOnce(ctx context.Context) bool {
	u.mu.Lock()
	if u.gen == u.doneGen {
		u.mu.Unlock()
		return false
	}
	gen, prev, target := u.gen, u.confirmed, u.desired
	u.mu.Unlock()

	sctx, cancel := context.WithTimeout(ctx, sendTimeout)
	err := u.dev.Send(sctx, prev, target)
	cancel()

	u.mu.Lock()
	var done []waiter
	if err == nil {
		u.confirmed, u.lastErr, u.doneGen = target, "", gen
		keep := u.waiters[:0]
		for _, w := range u.waiters {
			if w.gen <= gen {
				done = append(done, w)
			} else {
				keep = append(keep, w)
			}
		}
		u.waiters = keep
	} else {
		// Later changes were built on the failed state, so they fail too and
		// the state goes back to what the unit last accepted.
		log.Printf("%s: send failed: %v", u.cfg.ID, err)
		u.desired, u.lastErr, u.doneGen = u.confirmed, err.Error(), u.gen
		done, u.waiters = u.waiters, nil
	}
	u.mu.Unlock()
	for _, w := range done {
		w.ch <- err
	}
	u.onChange()
	return true
}

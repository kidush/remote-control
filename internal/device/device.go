// Package device defines how the daemon talks to an AC, and holds the
// implementations: the Tuya cloud and a fake for tests and UI work.
package device

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/kidush/remote-control/internal/ac"
	"github.com/kidush/remote-control/internal/tuya"
)

type Capabilities struct {
	// Swing is false when the backend cannot send swing at all, so the panel
	// hides the control instead of showing one that does nothing.
	Swing bool `json:"swing"`
	// Fetch is true when Fetch can read the state the backend last sent.
	Fetch bool `json:"fetch"`
}

// AC sends full states to an air conditioner. Send gets the previous state so
// a backend can pick the cheapest frame, but must leave the unit in next.
type AC interface {
	Send(ctx context.Context, prev, next ac.State) error
	Fetch(ctx context.Context) (ac.State, error)
	Capabilities() Capabilities
}

var ErrUnsupported = errors.New("not supported by this backend")

// Cloud drives an AC remote through the Tuya Cloud infrared API.
type Cloud struct {
	Client     *tuya.Client
	InfraredID string
	RemoteID   string
}

func (c *Cloud) Capabilities() Capabilities {
	// The Tuya AC API has power, mode, temp and wind only.
	return Capabilities{Swing: false, Fetch: true}
}

func (c *Cloud) Send(ctx context.Context, prev, next ac.State) error {
	if !next.Power {
		return c.Client.ACCommand(ctx, c.InfraredID, c.RemoteID, "power", "0")
	}
	return c.Client.ACScenes(ctx, c.InfraredID, c.RemoteID, tuya.ACScene{
		Power: "1",
		Mode:  tuya.ModeCodes[string(next.Mode)],
		// "24.0", the form the maintained Home Assistant integration sends.
		Temp: strconv.FormatFloat(float64(next.Temp), 'f', 1, 64),
		Wind: tuya.WindCodes[string(next.Fan)],
	})
}

func (c *Cloud) Fetch(ctx context.Context) (ac.State, error) {
	st, err := c.Client.ACStatus(ctx, c.InfraredID, c.RemoteID)
	if err != nil {
		return ac.State{}, err
	}
	temp, err := strconv.ParseFloat(string(st.Temp), 64)
	if err != nil {
		return ac.State{}, fmt.Errorf("tuya status: bad temperature %q", st.Temp)
	}
	return ac.State{
		Power: st.Power == "1",
		Mode:  ac.Mode(tuya.ModeNames[string(st.Mode)]),
		Temp:  int(math.Round(temp)),
		Fan:   ac.Speed(tuya.WindNames[string(st.Wind)]),
	}, nil
}

// Fake records what it is sent. Delay simulates cloud latency. FailNext, or
// FailFile existing on disk, makes sends fail, to exercise error handling.
type Fake struct {
	Delay    time.Duration
	FailFile string

	mu       sync.Mutex
	sent     []ac.State
	failNext int
}

func (f *Fake) Capabilities() Capabilities { return Capabilities{Swing: true, Fetch: false} }

func (f *Fake) Send(ctx context.Context, _, next ac.State) error {
	select {
	case <-time.After(f.Delay):
	case <-ctx.Done():
		return ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext > 0 {
		f.failNext--
		return errors.New("fake blaster: simulated failure")
	}
	if f.FailFile != "" {
		if _, err := os.Stat(f.FailFile); err == nil {
			return errors.New("fake blaster: offline (" + f.FailFile + " exists)")
		}
	}
	f.sent = append(f.sent, next)
	return nil
}

func (f *Fake) Fetch(context.Context) (ac.State, error) { return ac.State{}, ErrUnsupported }

func (f *Fake) FailNext(n int) {
	f.mu.Lock()
	f.failNext = n
	f.mu.Unlock()
}

func (f *Fake) Sent() []ac.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ac.State(nil), f.sent...)
}

// Package ac models the state of an IR air conditioner and the changes a user
// can make to it. IR is one-way, so this state is what was last sent, not what
// the unit reports.
package ac

import (
	"fmt"
	"slices"
)

type Mode string

const (
	Cool Mode = "cool"
	Heat Mode = "heat"
	Dry  Mode = "dry"
	Fan  Mode = "fan"
	Auto Mode = "auto"
)

// Modes lists every mode in the order the panel shows them.
var Modes = []Mode{Cool, Heat, Dry, Fan, Auto}

type Speed string

const (
	SpeedAuto Speed = "auto"
	SpeedLow  Speed = "low"
	SpeedMid  Speed = "mid"
	SpeedHigh Speed = "high"
)

// Speeds lists every fan speed in cycle order.
var Speeds = []Speed{SpeedAuto, SpeedLow, SpeedMid, SpeedHigh}

type State struct {
	Power bool  `json:"power"`
	Mode  Mode  `json:"mode"`
	Temp  int   `json:"temp"`
	Fan   Speed `json:"fan"`
	Swing bool  `json:"swing"`
}

type Limits struct {
	MinTemp int `json:"min_temp"`
	MaxTemp int `json:"max_temp"`
}

var DefaultLimits = Limits{MinTemp: 16, MaxTemp: 30}

func Default() State {
	return State{Mode: Cool, Temp: 24, Fan: SpeedAuto}
}

// Normalize fills missing fields and clamps the temperature, so a state read
// from an old or hand-edited file is always sendable.
func (s State) Normalize(lim Limits) State {
	if !slices.Contains(Modes, s.Mode) {
		s.Mode = Cool
	}
	if !slices.Contains(Speeds, s.Fan) {
		s.Fan = SpeedAuto
	}
	if s.Temp == 0 {
		s.Temp = 24
	}
	s.Temp = max(lim.MinTemp, min(lim.MaxTemp, s.Temp))
	return s
}

// Change is a user request against the current state. Pointer fields set a
// value; the boolean and delta fields act relative to the current state.
type Change struct {
	Power       *bool  `json:"power,omitempty"`
	TogglePower bool   `json:"toggle_power,omitempty"`
	Mode        *Mode  `json:"mode,omitempty"`
	Temp        *int   `json:"temp,omitempty"`
	TempDelta   int    `json:"temp_delta,omitempty"`
	Fan         *Speed `json:"fan,omitempty"`
	CycleFan    bool   `json:"cycle_fan,omitempty"`
	Swing       *bool  `json:"swing,omitempty"`
	ToggleSwing bool   `json:"toggle_swing,omitempty"`
}

func (c Change) IsZero() bool {
	return c == Change{}
}

// Apply returns the state after the change. Temperatures outside the limits
// are clamped for relative steps and rejected for absolute values.
func (s State) Apply(c Change, lim Limits) (State, error) {
	if c.Power != nil {
		s.Power = *c.Power
	}
	if c.TogglePower {
		s.Power = !s.Power
	}
	if c.Mode != nil {
		if !slices.Contains(Modes, *c.Mode) {
			return s, fmt.Errorf("unknown mode %q (want one of %v)", *c.Mode, Modes)
		}
		s.Mode = *c.Mode
	}
	if c.Temp != nil {
		if *c.Temp < lim.MinTemp || *c.Temp > lim.MaxTemp {
			return s, fmt.Errorf("temperature %d is outside %d-%d", *c.Temp, lim.MinTemp, lim.MaxTemp)
		}
		s.Temp = *c.Temp
	}
	if c.TempDelta != 0 {
		s.Temp = max(lim.MinTemp, min(lim.MaxTemp, s.Temp+c.TempDelta))
	}
	if c.Fan != nil {
		if !slices.Contains(Speeds, *c.Fan) {
			return s, fmt.Errorf("unknown fan speed %q (want one of %v)", *c.Fan, Speeds)
		}
		s.Fan = *c.Fan
	}
	if c.CycleFan {
		i := slices.Index(Speeds, s.Fan)
		s.Fan = Speeds[(i+1)%len(Speeds)]
	}
	if c.Swing != nil {
		s.Swing = *c.Swing
	}
	if c.ToggleSwing {
		s.Swing = !s.Swing
	}
	return s, nil
}

// Preset is a named full setting. Applying it always turns the unit on.
type Preset struct {
	Name  string `json:"name" toml:"name"`
	Mode  Mode   `json:"mode" toml:"mode"`
	Temp  int    `json:"temp" toml:"temp"`
	Fan   Speed  `json:"fan" toml:"fan"`
	Swing *bool  `json:"swing,omitempty" toml:"swing"`
}

func (p Preset) Change() Change {
	on := true
	c := Change{Power: &on}
	if p.Mode != "" {
		m := p.Mode
		c.Mode = &m
	}
	if p.Temp != 0 {
		t := p.Temp
		c.Temp = &t
	}
	if p.Fan != "" {
		f := p.Fan
		c.Fan = &f
	}
	c.Swing = p.Swing
	return c
}

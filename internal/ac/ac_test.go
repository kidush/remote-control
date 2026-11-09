package ac

import "testing"

func ptr[T any](v T) *T { return &v }

func TestApply(t *testing.T) {
	lim := DefaultLimits
	base := State{Power: true, Mode: Cool, Temp: 24, Fan: SpeedAuto}

	cases := []struct {
		name   string
		start  State
		change Change
		want   State
		err    bool
	}{
		{"toggle power", base, Change{TogglePower: true}, State{Mode: Cool, Temp: 24, Fan: SpeedAuto}, false},
		{"temp up", base, Change{TempDelta: 1}, State{Power: true, Mode: Cool, Temp: 25, Fan: SpeedAuto}, false},
		{"temp up clamps at max", State{Power: true, Mode: Cool, Temp: 30, Fan: SpeedAuto}, Change{TempDelta: 1}, State{Power: true, Mode: Cool, Temp: 30, Fan: SpeedAuto}, false},
		{"temp down clamps at min", State{Power: true, Mode: Cool, Temp: 16, Fan: SpeedAuto}, Change{TempDelta: -1}, State{Power: true, Mode: Cool, Temp: 16, Fan: SpeedAuto}, false},
		{"absolute temp out of range", base, Change{Temp: ptr(31)}, base, true},
		{"fan cycles and wraps", State{Power: true, Mode: Cool, Temp: 24, Fan: SpeedHigh}, Change{CycleFan: true}, base, false},
		{"bad mode", base, Change{Mode: ptr(Mode("turbo"))}, base, true},
		{"bad fan", base, Change{Fan: ptr(Speed("max"))}, base, true},
		{"swing toggle", base, Change{ToggleSwing: true}, State{Power: true, Mode: Cool, Temp: 24, Fan: SpeedAuto, Swing: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.start.Apply(tc.change, lim)
			if (err != nil) != tc.err {
				t.Fatalf("err = %v, want error %v", err, tc.err)
			}
			if err == nil && got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestPresetTurnsOn(t *testing.T) {
	p := Preset{Name: "Dormir", Mode: Cool, Temp: 26, Fan: SpeedLow}
	got, err := Default().Apply(p.Change(), DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	want := State{Power: true, Mode: Cool, Temp: 26, Fan: SpeedLow}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestNormalize(t *testing.T) {
	got := State{Mode: "weird", Temp: 99}.Normalize(DefaultLimits)
	want := State{Mode: Cool, Temp: 30, Fan: SpeedAuto}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

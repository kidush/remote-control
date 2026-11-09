// Package config loads the user's settings and Tuya credentials.
//
// Settings live in ~/.config/remote-control/config.toml and can be shared in
// dotfiles. Secrets live in credentials.toml next to it and must be mode 600.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/kidush/remote-control/internal/ac"
)

type Device struct {
	ID      string      `toml:"id"`
	Name    string      `toml:"name"`
	MinTemp int         `toml:"min_temp"`
	MaxTemp int         `toml:"max_temp"`
	Presets []ac.Preset `toml:"presets"`
}

func (d Device) Limits() ac.Limits {
	return ac.Limits{MinTemp: d.MinTemp, MaxTemp: d.MaxTemp}
}

type Config struct {
	Devices []Device `toml:"devices"`
}

type Credentials struct {
	Region       string `toml:"region"`
	AccessID     string `toml:"access_id"`
	AccessSecret string `toml:"access_secret"`
	// Remotes maps a device id from config.toml to its Tuya IR hub and remote.
	Remotes map[string]Remote `toml:"remotes"`
}

type Remote struct {
	InfraredID string `toml:"infrared_id"`
	RemoteID   string `toml:"remote_id"`
}

// Missing lists the credential fields still empty for a device, so the daemon
// can report exactly what setup step is left.
func (c Credentials) Missing(deviceID string) []string {
	var out []string
	if c.AccessID == "" {
		out = append(out, "access_id")
	}
	if c.AccessSecret == "" {
		out = append(out, "access_secret")
	}
	r := c.Remotes[deviceID]
	if r.InfraredID == "" {
		out = append(out, "remotes."+deviceID+".infrared_id")
	}
	if r.RemoteID == "" {
		out = append(out, "remotes."+deviceID+".remote_id")
	}
	return out
}

func Dir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "remote-control")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "remote-control")
}

func StateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "remote-control")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "remote-control")
}

func RuntimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "remote-control")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("remote-control-%d", os.Getuid()))
}

// Default is the configuration used when config.toml does not exist.
func Default() Config {
	swingOff := false
	return Config{Devices: []Device{{
		ID:      "ac",
		Name:    "Air conditioner",
		MinTemp: ac.DefaultLimits.MinTemp,
		MaxTemp: ac.DefaultLimits.MaxTemp,
		Presets: []ac.Preset{
			{Name: "Sleep", Mode: ac.Cool, Temp: 24, Fan: ac.SpeedLow, Swing: &swingOff},
			{Name: "Quick cool", Mode: ac.Cool, Temp: 18, Fan: ac.SpeedHigh},
		},
	}}}
}

func Load(path string) (Config, error) {
	var cfg Config
	_, err := toml.DecodeFile(path, &cfg)
	if errors.Is(err, fs.ErrNotExist) {
		cfg = Default()
	} else if err != nil {
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	if len(cfg.Devices) == 0 {
		return cfg, fmt.Errorf("%s defines no devices", path)
	}
	seen := map[string]bool{}
	for i := range cfg.Devices {
		d := &cfg.Devices[i]
		if d.ID == "" || strings.ContainsAny(d.ID, " /") {
			return cfg, fmt.Errorf("%s: device %d needs an id without spaces or slashes", path, i+1)
		}
		if seen[d.ID] {
			return cfg, fmt.Errorf("%s: duplicate device id %q", path, d.ID)
		}
		seen[d.ID] = true
		if d.Name == "" {
			d.Name = d.ID
		}
		if d.MinTemp == 0 {
			d.MinTemp = ac.DefaultLimits.MinTemp
		}
		if d.MaxTemp == 0 {
			d.MaxTemp = ac.DefaultLimits.MaxTemp
		}
		if d.MinTemp > d.MaxTemp {
			return cfg, fmt.Errorf("%s: device %q has min_temp above max_temp", path, d.ID)
		}
		for _, p := range d.Presets {
			if _, err := ac.Default().Apply(p.Change(), d.Limits()); err != nil {
				return cfg, fmt.Errorf("%s: preset %q: %w", path, p.Name, err)
			}
		}
	}
	return cfg, nil
}

// LoadCredentials returns empty credentials when the file does not exist, so
// the daemon can start and report that setup is needed. It refuses a file other
// users can read.
func LoadCredentials(path string) (Credentials, error) {
	var c Credentials
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return c, fmt.Errorf("%s is readable by other users; run: chmod 600 %s", path, path)
	}
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return c, fmt.Errorf("read %s: %w", path, err)
	}
	if c.Region == "" {
		c.Region = "us"
	}
	return c, nil
}

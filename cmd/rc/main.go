// Command rc controls IR devices (an AC through a Tuya IR blaster) from the
// desktop. "rc daemon" owns the device; every other command talks to it.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kidush/remote-control/internal/ac"
	"github.com/kidush/remote-control/internal/config"
	"github.com/kidush/remote-control/internal/daemon"
	"github.com/kidush/remote-control/internal/device"
	"github.com/kidush/remote-control/internal/tuya"
)

const usage = `Usage: rc [--fake] [--notify] <command>

Daemon and status:
  rc daemon                       run the daemon (normally via systemd)
  rc status [--json]              show every device
  rc watch                        stream status as JSON lines

Devices (the id comes from config.toml, "ac" by default):
  rc ac on | off | toggle
  rc ac temp <16-30> | up | down
  rc ac mode cool | heat | dry | fan | auto
  rc ac fan auto | low | mid | high | cycle
  rc ac swing on | off | toggle
  rc ac preset <name>
  rc ac resend                    send the shown state again (after using the physical remote)
  rc ac fetch                     load the state the Tuya cloud last sent

Tuya setup helpers:
  rc tuya remotes [device]        list the remotes under the IR hub
  rc tuya keys [device]           show the raw key list of a remote

Flags:
  --fake     use a fake blaster with its own socket and state, for testing
  --notify   show a desktop notification with the result (for keybindings)
`

type paths struct {
	config, credentials, state, socket, failFile string
}

func resolvePaths(fake bool) paths {
	p := paths{
		config:      filepath.Join(config.Dir(), "config.toml"),
		credentials: filepath.Join(config.Dir(), "credentials.toml"),
		state:       filepath.Join(config.StateDir(), "state.json"),
		socket:      filepath.Join(config.RuntimeDir(), "rc.sock"),
	}
	if fake {
		p.state = filepath.Join(config.StateDir(), "fake-state.json")
		p.socket = filepath.Join(config.RuntimeDir(), "fake.sock")
		p.failFile = filepath.Join(config.RuntimeDir(), "fake-fail")
	}
	return p
}

func main() {
	fs := flag.NewFlagSet("rc", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	fake := fs.Bool("fake", false, "")
	notify := fs.Bool("notify", false, "")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	args := fs.Args()
	if len(args) == 0 {
		fs.Usage()
		os.Exit(2)
	}
	p := resolvePaths(*fake)
	if err := run(p, *fake, *notify, args); err != nil {
		fmt.Fprintln(os.Stderr, "rc:", err)
		os.Exit(1)
	}
}

func run(p paths, fake, notify bool, args []string) error {
	switch args[0] {
	case "daemon":
		return runDaemon(p, fake)
	case "status":
		resp, err := daemon.Do(p.socket, daemon.Request{Op: "status"})
		if err != nil {
			return err
		}
		if len(args) > 1 && args[1] == "--json" {
			return json.NewEncoder(os.Stdout).Encode(resp)
		}
		for _, d := range resp.Devices {
			fmt.Println(describeLine(d))
		}
		return nil
	case "watch":
		enc := json.NewEncoder(os.Stdout)
		return daemon.Watch(p.socket, func(r daemon.Response) error { return enc.Encode(r) })
	case "tuya":
		return runTuya(p, args[1:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	}
	req, err := parseDeviceCommand(args)
	if err != nil {
		return err
	}
	resp, err := daemon.Do(p.socket, req)
	if notify {
		sendNotification(req.Device, resp, err)
	}
	if err != nil {
		return err
	}
	for _, d := range resp.Devices {
		if d.ID == req.Device {
			fmt.Println(describeLine(d))
		}
	}
	return nil
}

func parseDeviceCommand(args []string) (daemon.Request, error) {
	if len(args) < 2 {
		return daemon.Request{}, fmt.Errorf("missing action for %q (see rc help)", args[0])
	}
	req := daemon.Request{Op: "change", Device: args[0]}
	action, rest := args[1], args[2:]
	arg := ""
	if len(rest) > 0 {
		arg = rest[0]
	}
	var c ac.Change
	t, f := true, false
	switch action {
	case "on":
		c.Power = &t
	case "off":
		c.Power = &f
	case "toggle":
		c.TogglePower = true
	case "temp":
		switch arg {
		case "up", "+":
			c.TempDelta = 1
		case "down", "-":
			c.TempDelta = -1
		default:
			n, err := strconv.Atoi(arg)
			if err != nil {
				return req, fmt.Errorf("temp wants a number, up or down, not %q", arg)
			}
			c.Temp = &n
		}
	case "mode":
		m := ac.Mode(arg)
		c.Mode = &m
	case "fan":
		if arg == "cycle" {
			c.CycleFan = true
		} else {
			s := ac.Speed(arg)
			c.Fan = &s
		}
	case "swing":
		switch arg {
		case "on":
			c.Swing = &t
		case "off":
			c.Swing = &f
		case "toggle", "":
			c.ToggleSwing = true
		default:
			return req, fmt.Errorf("swing wants on, off or toggle, not %q", arg)
		}
	case "preset":
		if len(rest) == 0 {
			return req, errors.New("preset wants a name")
		}
		return daemon.Request{Op: "preset", Device: args[0], Preset: strings.Join(rest, " ")}, nil
	case "resend":
		return daemon.Request{Op: "resend", Device: args[0]}, nil
	case "fetch":
		return daemon.Request{Op: "fetch", Device: args[0]}, nil
	default:
		return req, fmt.Errorf("unknown action %q (see rc help)", action)
	}
	if (action == "mode" || action == "fan") && arg == "" {
		return req, fmt.Errorf("%s wants a value (see rc help)", action)
	}
	req.Change = &c
	return req, nil
}

func runDaemon(p paths, fake bool) error {
	log.SetFlags(0)
	cfg, err := config.Load(p.config)
	if err != nil {
		return err
	}
	var creds config.Credentials
	var credErr error
	if !fake {
		creds, credErr = config.LoadCredentials(p.credentials)
	}
	store := &daemon.Store{Path: p.state}
	saved, err := store.Load()
	if err != nil {
		log.Printf("ignoring unreadable state file: %v", err)
	}
	d := daemon.New(store)
	var client *tuya.Client
	for _, dc := range cfg.Devices {
		var dev device.AC
		setup := ""
		switch {
		case fake:
			delay := 600 * time.Millisecond
			if v, err := time.ParseDuration(os.Getenv("RC_FAKE_DELAY")); err == nil {
				delay = v
			}
			dev = &device.Fake{Delay: delay, FailFile: p.failFile}
		case credErr != nil:
			setup = credErr.Error()
		case len(creds.Missing(dc.ID)) > 0:
			setup = fmt.Sprintf("fill %s in %s", strings.Join(creds.Missing(dc.ID), ", "), p.credentials)
		default:
			if client == nil {
				if client, err = tuya.NewClient(creds.Region, creds.AccessID, creds.AccessSecret); err != nil {
					setup = err.Error()
					break
				}
			}
			r := creds.Remotes[dc.ID]
			dev = &device.Cloud{Client: client, InfraredID: r.InfraredID, RemoteID: r.RemoteID}
		}
		if setup != "" {
			log.Printf("%s: %s", dc.ID, setup)
		}
		state, ok := saved[dc.ID]
		if !ok {
			state = ac.Default()
		}
		d.Add(daemon.NewUnit(dc, dev, setup, state, d.Changed))
	}

	ln, err := daemon.Listen(p.socket)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Printf("listening on %s", p.socket)
	return d.Serve(ctx, ln)
}

func runTuya(p paths, args []string) error {
	if len(args) == 0 {
		return errors.New("tuya wants remotes or keys")
	}
	id := "ac"
	if len(args) > 1 {
		id = args[1]
	}
	creds, err := config.LoadCredentials(p.credentials)
	if err != nil {
		return err
	}
	if creds.AccessID == "" || creds.AccessSecret == "" {
		return fmt.Errorf("fill access_id and access_secret in %s first", p.credentials)
	}
	r := creds.Remotes[id]
	if r.InfraredID == "" {
		return fmt.Errorf("fill remotes.%s.infrared_id in %s first", id, p.credentials)
	}
	client, err := tuya.NewClient(creds.Region, creds.AccessID, creds.AccessSecret)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	switch args[0] {
	case "remotes":
		remotes, err := client.Remotes(ctx, r.InfraredID)
		if err != nil {
			return err
		}
		if len(remotes) == 0 {
			fmt.Println("No remotes under this IR hub. Add the AC remote in the app first.")
		}
		for _, rm := range remotes {
			kind := ""
			if rm.CategoryID == "5" {
				kind = " (air conditioner)"
			}
			fmt.Printf("remote_id = %q   # %s, %s%s\n", rm.RemoteID, rm.RemoteName, rm.BrandName, kind)
		}
		return nil
	case "keys":
		if r.RemoteID == "" {
			return fmt.Errorf("fill remotes.%s.remote_id in %s first", id, p.credentials)
		}
		raw, err := client.ACKeys(ctx, r.InfraredID, r.RemoteID)
		if err != nil {
			return err
		}
		var pretty any
		if json.Unmarshal(raw, &pretty) == nil {
			return json.NewEncoder(os.Stdout).Encode(pretty)
		}
		fmt.Println(string(raw))
		return nil
	}
	return fmt.Errorf("unknown tuya command %q", args[0])
}

// Strings for status lines and notifications, following the language picked
// in Omarchy (the same catalogs the bar widget uses).
type lang struct {
	modes                       map[ac.Mode]string
	speeds                      map[ac.Speed]string
	off, on, failed, app, setup string
}

var langs = map[string]lang{
	"en": {
		modes:  map[ac.Mode]string{ac.Cool: "Cool", ac.Heat: "Heat", ac.Dry: "Dry", ac.Fan: "Fan", ac.Auto: "Auto"},
		speeds: map[ac.Speed]string{ac.SpeedAuto: "auto", ac.SpeedLow: "low", ac.SpeedMid: "medium", ac.SpeedHigh: "high"},
		off:    "Off", on: "%s %d°C · fan %s", failed: "Failed: %s", app: "Remote control", setup: "setup needed",
	},
	"pt": {
		modes:  map[ac.Mode]string{ac.Cool: "Frio", ac.Heat: "Calor", ac.Dry: "Seco", ac.Fan: "Ventilar", ac.Auto: "Auto"},
		speeds: map[ac.Speed]string{ac.SpeedAuto: "auto", ac.SpeedLow: "baixa", ac.SpeedMid: "média", ac.SpeedHigh: "alta"},
		off:    "Desligado", on: "%s %d°C · ventilação %s", failed: "Falhou: %s", app: "Controle remoto", setup: "configuração pendente",
	},
}

func currentLang() lang {
	code := ""
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(config.Dir()), "omarchy", "language")); err == nil {
		code = strings.TrimSpace(string(b))
	}
	if l, ok := langs[strings.SplitN(code, "-", 2)[0]]; ok {
		return l
	}
	return langs["en"]
}

func describeState(s ac.State) string {
	l := currentLang()
	if !s.Power {
		return l.off
	}
	return fmt.Sprintf(l.on, l.modes[s.Mode], s.Temp, l.speeds[s.Fan])
}

func describeLine(d daemon.Snapshot) string {
	line := fmt.Sprintf("%s: %s", d.Name, describeState(d.State))
	if d.Setup != "" {
		line += " [setup needed: " + d.Setup + "]"
	} else if d.Error != "" {
		line += " [last command failed: " + d.Error + "]"
	}
	return line
}

// sendNotification replaces this device's previous notification instead of
// stacking a new one per key press.
func sendNotification(id string, resp daemon.Response, err error) {
	l := currentLang()
	title, body, urgency := l.app, "", "normal"
	for _, d := range resp.Devices {
		if d.ID == id {
			title = d.Name
			body = describeState(d.State)
		}
	}
	if err != nil {
		urgency = "critical"
		body = fmt.Sprintf(l.failed, err.Error())
	}
	idFile := filepath.Join(config.RuntimeDir(), "notify-"+id)
	args := []string{"-p", "-a", l.app, "-u", urgency, "-i", "weather-snow-symbolic"}
	if b, err := os.ReadFile(idFile); err == nil {
		if n := strings.TrimSpace(string(b)); n != "" {
			args = append(args, "-r", n)
		}
	}
	out, nerr := exec.Command("notify-send", append(args, title, body)...).Output()
	if nerr == nil {
		_ = os.MkdirAll(filepath.Dir(idFile), 0o700)
		_ = os.WriteFile(idFile, out, 0o600)
	}
}

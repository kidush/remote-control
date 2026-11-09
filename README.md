# remote-control

Control an IR air conditioner from the Omarchy bar, the command line, and
keyboard shortcuts. It talks to a Tuya IR blaster through the Tuya Cloud API.
Tested with a Positivo Casa Inteligente PCUIRN driving a Komeco AC.

## Install

```sh
./install.sh
```

The script:

- Builds `rc` into `~/.local/bin/rc`.
- Installs the `kidush.remote-control` bar widget and enables it in the right
  section.
- Installs and starts the `remote-control` user service.
- Writes starter `config.toml` and `credentials.toml` to
  `~/.config/remote-control/`.

It backs up the plugin and `shell.json` before changing them and never
overwrites your config.

Then follow [docs/tuya-setup.md](docs/tuya-setup.md) to connect your Tuya
account. Until you do, the bar shows a red warning and every command answers
"setup needed".

## Use

| Where | What |
|-------|------|
| Bar | Snowflake icon, showing the temperature when the AC is on and a red warning when the last command failed. Click it for power, mode, temperature, fan, swing (when supported) and presets. |
| `Super+Ctrl+Alt+A` | AC on/off |
| `Super+Ctrl+Alt+Up` / `Down` | Temperature up/down by 1°C |
| `Super+Ctrl+Alt+V` | Cycle the fan speed |
| CLI | `rc help` |
| IPC | `omarchy-shell kidush.remote-control openDevice ac` opens the AC controls directly |

The shortcuts live in `~/.config/hypr/bindings.lua` and show a notification
with the new state.

```sh
rc status
rc ac on | off | toggle
rc ac temp 23 | up | down
rc ac mode cool | heat | dry | fan | auto
rc ac fan auto | low | mid | high | cycle
rc ac preset Sleep
rc ac resend      # after someone used the physical remote
rc ac fetch       # load the state the Tuya cloud last sent
```

### What the state means

IR is one-way, so the AC never reports back. The state you see is what was
last sent. A failed command leaves it unchanged and shows the error.

- **Physical remote:** changes made with it are invisible here. Use
  **Resend state** in the popup (`rc ac resend`) to push the shown state
  back to the AC.
- **Changes while off:** changing settings while the AC is off stores them
  without transmitting, the way a real remote behaves. They are sent on the
  next power on.
- **Swing:** Tuya's cloud API has no swing command for AC remotes, so the
  swing control is hidden with the cloud backend. `rc tuya keys` shows what
  your remote exposes.

## Configure

`~/.config/remote-control/config.toml` holds devices and presets (see
[contrib/config.example.toml](contrib/config.example.toml)). Restart the
daemon after editing it:

```sh
systemctl --user restart remote-control
```

Secrets go in `credentials.toml` (mode 600).

## Develop

```sh
go test -race ./...
rc --fake daemon &          # fake blaster: own socket and state, 600ms latency
rc --fake ac on
touch $XDG_RUNTIME_DIR/remote-control/fake-fail   # make sends fail
```

To point the bar widget at the fake daemon, add `"fake": true` to its entry
in `~/.config/omarchy/shell.json`.

### Layout

```
cmd/rc/            CLI and daemon entry point
internal/ac/       AC state, changes, presets
internal/daemon/   per-device send queue, state file, Unix socket server and client
internal/device/   backend interface: Tuya cloud, fake
internal/tuya/     Tuya OpenAPI client (signing, tokens, IR AC endpoints)
plugin/            Omarchy bar widget (QML)
contrib/           systemd unit, example config
```

The daemon keeps a desired state and a confirmed state per device. Each
change updates the desired state at once (the widget shows it immediately)
and wakes one sender. The sender always sends the newest desired state, so
quick key presses coalesce into fewer IR frames and never arrive out of
order. When a send fails, the state goes back to the last confirmed one.

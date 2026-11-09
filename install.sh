#!/usr/bin/env bash
# Build rc, install the daemon as a user service, and add the bar widget.
# Safe to run again after changes: it backs up what it replaces and never
# overwrites config.toml or credentials.toml.
set -euo pipefail
source_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
plugin_id=kidush.remote-control
config_home="${XDG_CONFIG_HOME:-$HOME/.config}"
state_home="${XDG_STATE_HOME:-$HOME/.local/state}"
plugin_target="$config_home/omarchy/plugins/$plugin_id"
rc_config="$config_home/remote-control"
stamp=$(date +%Y%m%d-%H%M%S)

cd "$source_dir"
echo "Building rc..."
go build -o bin/rc ./cmd/rc
install -D -m 755 bin/rc "$HOME/.local/bin/rc"

omarchy plugin validate "$source_dir/plugin"

# Back up an existing plugin install and the bar layout before changing them.
backup="$state_home/remote-control/backups/$stamp"
updating=false
if [[ -d "$plugin_target" ]]; then
  updating=true
  mkdir -p "$backup"
  cp -a -- "$plugin_target" "$backup/plugin"
fi
shell_json="$config_home/omarchy/shell.json"
[[ ! -f "$shell_json" ]] || cp -a -- "$shell_json" "${shell_json}.backup-${stamp}"

mkdir -p "$plugin_target/i18n"
install -m 644 plugin/manifest.json plugin/Panel.qml "$plugin_target/"
install -m 644 plugin/i18n/*.json "$plugin_target/i18n/"

# Starter config; the credentials file stays private.
mkdir -p "$rc_config"
[[ -f "$rc_config/config.toml" ]] || install -m 644 contrib/config.example.toml "$rc_config/config.toml"
[[ -f "$rc_config/credentials.toml" ]] || install -m 600 contrib/credentials.example.toml "$rc_config/credentials.toml"

install -D -m 644 contrib/remote-control.service "$config_home/systemd/user/remote-control.service"
systemctl --user daemon-reload
systemctl --user enable remote-control.service >/dev/null
systemctl --user restart remote-control.service

# A plugin already loaded keeps its old code through a rescan, so restart the
# shell when updating.
if $updating; then
  omarchy restart shell >/dev/null
else
  omarchy-shell shell rescanPlugins
fi
if ! grep -q "\"$plugin_id\"" "$shell_json" 2>/dev/null; then
  omarchy plugin enable "$plugin_id" --section right
fi

echo
echo "Installed. Daemon status: systemctl --user status remote-control"
rc status || true

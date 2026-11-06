#!/usr/bin/env bash
set -euo pipefail
source_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
[[ "$EUID" == 0 ]] || { printf 'Run with sudo on the intended Linux node.\n' >&2; exit 1; }
command -v python3 >/dev/null
command -v systemctl >/dev/null
if ! id bosapi-monitor >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin bosapi-monitor
fi
install -d -m 755 /usr/local/lib/bosapi-monitor
install -m 644 "$source_dir/collect.py" /usr/local/lib/bosapi-monitor/collect.py
install -d -m 755 -o bosapi-monitor -g bosapi-monitor /var/lib/bosapi-monitor
install -m 644 "$source_dir/bosapi-monitor.service" /etc/systemd/system/bosapi-monitor.service
install -m 644 "$source_dir/bosapi-monitor.timer" /etc/systemd/system/bosapi-monitor.timer
systemctl daemon-reload
systemctl enable --now bosapi-monitor.timer
systemctl start bosapi-monitor.service
systemctl --no-pager status bosapi-monitor.timer

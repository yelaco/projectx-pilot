#!/usr/bin/env bash
set -euo pipefail
if [[ "$(id -u)" == 0 ]]; then
  echo 'Run this as your ordinary desktop user, without sudo.' >&2
  exit 1
fi
named_hostname=""
named_token_file=""
dashboard_scope=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --remote-dashboard)
      [[ $# -ge 2 ]] || { echo "--remote-dashboard needs a private scope JSON file" >&2; exit 1; }
      dashboard_scope="$2"; shift 2 ;;
    --named-tunnel)
      [[ $# -ge 2 ]] || { echo 'Usage: --named-tunnel alerts.example.com [--token-file /private/file]' >&2; exit 1; }
      named_hostname="$2"; shift 2 ;;
    --token-file)
      [[ $# -ge 2 ]] || { echo '--token-file needs a path' >&2; exit 1; }
      named_token_file="$2"; shift 2 ;;
    *) echo 'Usage: ./scripts/install-user-service.sh [--named-tunnel alerts.example.com [--token-file /private/file]] [--remote-dashboard /private/scope.json]' >&2; exit 1 ;;
  esac
done
if [[ -n "$named_token_file" && -z "$named_hostname" ]]; then
  echo '--token-file requires --named-tunnel' >&2; exit 1
fi
# Do not create a second connector alongside a separately installed system service.
if [[ -n "$named_hostname" ]] && systemctl is-active --quiet cloudflared.service; then
  echo 'An existing system cloudflared connector is running. Stop it explicitly before switching to the bot-managed user service: sudo systemctl disable --now cloudflared' >&2
  exit 1
fi
project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$project_root"
# One setup command on Arch: install missing packages, then build and start both services.
if command -v pacman >/dev/null; then
  missing=0
  for package in go gcc pkgconf sqlite openssl cloudflared; do
    if ! pacman -Q "$package" >/dev/null 2>&1; then missing=1; fi
  done
  if [[ "$missing" == 1 ]]; then
    sudo pacman -S --needed --noconfirm go gcc pkgconf sqlite openssl cloudflared
  fi
fi
if ! command -v go >/dev/null || ! command -v pkg-config >/dev/null || ! pkg-config --exists sqlite3 || ! command -v gcc >/dev/null; then
  echo 'Required: Go, GCC, pkgconf, SQLite headers. On Arch rerun with sudo available.' >&2
  exit 1
fi
if [[ "$(go env CGO_ENABLED)" != 1 ]]; then
  echo 'SQLite requires CGO_ENABLED=1.' >&2
  exit 1
fi
go test ./...
go vet ./...
go build -buildvcs=false -o bin/bot ./cmd/bot
go build -buildvcs=false -o bin/webhook ./cmd/webhook
if [[ -n "$dashboard_scope" ]]; then
  [[ -r "$dashboard_scope" ]] || { echo "Remote dashboard scope is unreadable" >&2; exit 1; }
  go build -buildvcs=false -o bin/configure-dashboard ./cmd/configure-dashboard
  bin/configure-dashboard -check -config "$HOME/.config/projectx-bot/config.yaml" -state "$HOME/.local/share/projectx-bot" < "$dashboard_scope"
fi
named_temp=""
trap '[[ -z "$named_temp" ]] || rm -f -- "$named_temp"' EXIT
if [[ -n "$named_hostname" ]]; then
  go build -buildvcs=false -o bin/configure-tunnel ./cmd/configure-tunnel
  if ! command -v cloudflared >/dev/null || ! cloudflared tunnel run --help 2>&1 | grep -- '--token-file' >/dev/null; then
    echo 'Named setup needs cloudflared 2025.4.0+ with --token-file support.' >&2; exit 1
  fi
  if [[ -z "$named_token_file" && -s "$HOME/.config/projectx-bot/cloudflared-token" ]]; then
    named_token_file="$HOME/.config/projectx-bot/cloudflared-token"
  fi
  if [[ -z "$named_token_file" ]]; then
    # Hide input, disable tracing, and keep the secret out of command arguments.
    set +x
    read -r -s -p 'Paste only the Cloudflare tunnel token (hidden): ' named_token
    printf '\n'
    named_temp=$(mktemp)
    chmod 600 "$named_temp"
    printf '%s' "$named_token" > "$named_temp"
    unset named_token
    named_token_file="$named_temp"
  fi
  [[ -r "$named_token_file" && -s "$named_token_file" ]] || { echo 'Tunnel token file is empty or unreadable.' >&2; exit 1; }
  bin/configure-tunnel -check -hostname "$named_hostname" -state "$HOME/.local/share/projectx-bot" -config-dir "$HOME/.config/projectx-bot" < "$named_token_file"
fi
install -d -m 700 "$HOME/.config/projectx-bot" "$HOME/.local/share/projectx-bot"
install -d "$HOME/.local/bin" "$HOME/.config/systemd/user"
# Never upgrade over a bot-managed order or an unresolved submission.
execution_db="$HOME/.local/share/projectx-bot/execution.db"
if [[ -f "$execution_db" ]]; then
  if ! command -v sqlite3 >/dev/null; then
    echo 'Cannot verify execution state: install sqlite3 before upgrading.' >&2
    exit 1
  fi
  if ! active_executions=$(sqlite3 -readonly "$execution_db" "SELECT COUNT(*) FROM execution_intents WHERE state NOT IN ('closed','rejected','cancelled');"); then
    echo 'Cannot verify execution ledger. Keep the existing service running and inspect TopstepX.' >&2
    exit 1
  fi
  if [[ "$active_executions" != 0 ]]; then
    echo 'Upgrade refused: a trade or unresolved execution exists. Verify flatness and resolve it in Practice execution first.' >&2
    exit 1
  fi
fi
bot_was_running=0
# Stop an existing service before replacing its executable or persisted settings.
if systemctl --user is-active --quiet projectx-bot.service; then
  bot_was_running=1
  systemctl --user stop projectx-bot.service
fi
if systemctl --user is-active --quiet projectx-webhook.service; then
  systemctl --user stop projectx-webhook.service
fi
# Recheck after shutdown: an armed automatic alert may have arrived between the
# first read and stopping the old service. Resume the old monitor instead of
# replacing a binary while an order needs recovery.
if [[ -f "$execution_db" ]]; then
  active_executions=$(sqlite3 -readonly "$execution_db" "SELECT COUNT(*) FROM execution_intents WHERE state NOT IN ('closed','rejected','cancelled');") || active_executions=unknown
  if [[ "$active_executions" != 0 ]]; then
    if [[ "$bot_was_running" == 1 ]]; then
      systemctl --user start projectx-bot.service projectx-webhook.service
    fi
    echo 'Upgrade stopped: execution appeared during shutdown. The previous monitor was resumed when applicable; inspect TopstepX and the execution page.' >&2
    exit 1
  fi
fi
if [[ -n "$named_hostname" ]]; then
  # No service is started if local preparation fails. No Cloudflare API mutation.
  bin/configure-tunnel -hostname "$named_hostname" -state "$HOME/.local/share/projectx-bot" -config-dir "$HOME/.config/projectx-bot" < "$named_token_file"
  install -m 644 deploy/systemd/projectx-tunnel.service "$HOME/.config/systemd/user/projectx-tunnel.service"
fi
install -m 755 bin/bot "$HOME/.local/bin/projectx-bot"
install -m 755 bin/webhook "$HOME/.local/bin/projectx-webhook"
if [[ ! -e "$HOME/.config/projectx-bot/config.yaml" ]]; then
  install -m 600 config/example.yaml "$HOME/.config/projectx-bot/config.yaml"
fi
if [[ -n "$dashboard_scope" ]]; then
  bin/configure-dashboard -config "$HOME/.config/projectx-bot/config.yaml" -state "$HOME/.local/share/projectx-bot" < "$dashboard_scope"
fi
install -m 644 deploy/systemd/projectx-bot.service "$HOME/.config/systemd/user/projectx-bot.service"
install -m 644 deploy/systemd/projectx-webhook.service "$HOME/.config/systemd/user/projectx-webhook.service"
systemctl --user daemon-reload
systemctl --user enable --now projectx-bot.service projectx-webhook.service
if [[ -n "$named_hostname" ]]; then
  systemctl --user enable projectx-tunnel.service
  systemctl --user restart projectx-tunnel.service
  if ! systemctl --user is-active --quiet projectx-tunnel.service; then
    echo 'Named tunnel failed; inspect: journalctl --user -u projectx-tunnel -n 40' >&2; exit 1
  fi
  printf 'Permanent webhook base URL: https://%s\n' "$named_hostname"
  printf 'Tunnel logs: journalctl --user -u projectx-tunnel -f\n'
fi
for attempt in {1..30}; do
  if [[ -f "$HOME/.local/share/projectx-bot/admin-password" || -f "$HOME/.local/share/projectx-bot/admins.json" ]]; then break; fi
  sleep 0.1
done
if [[ -f "$HOME/.local/share/projectx-bot/admin-password" ]]; then
  printf 'Initial password file: %s/.local/share/projectx-bot/admin-password (existing saved logins take precedence).\n' "$HOME"
fi
printf 'Admin page: http://127.0.0.1:8080\nSign in with your saved admin login (initial username: local). Manage logins under Settings -> Admin access.\n'
if ! systemctl --user is-active --quiet projectx-bot.service || ! systemctl --user is-active --quiet projectx-webhook.service; then
  echo 'A service failed. Check: journalctl --user -u projectx-bot -u projectx-webhook -n 40' >&2
  exit 1
fi
printf 'Configure credentials, per-profile webhook tokens/templates, receiving and the managed test tunnel in the admin page.\n'
printf 'If old manually started receiver/tunnel processes are running, stop those terminals to release port 8090.\n'
printf 'View status: systemctl --user status projectx-bot.service\n'

if command -v xdg-open >/dev/null && [[ -n "${WAYLAND_DISPLAY:-}${DISPLAY:-}" ]]; then
  xdg-open http://127.0.0.1:8080/ >/dev/null 2>&1 &
fi

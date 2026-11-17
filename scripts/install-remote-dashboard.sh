#!/usr/bin/env bash
# Install on the user-selected machine; no machine-specific activation flag.
set -euo pipefail
scope_file=""
credential_file=""
check_only=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --check) check_only=1; shift ;;
    --access-scope) [[ $# -ge 2 ]] || exit 1; scope_file="$2"; shift 2 ;;
    --tunnel-credentials) [[ $# -ge 2 ]] || exit 1; credential_file="$2"; shift 2 ;;
    *) echo 'Usage: install-remote-dashboard.sh --access-scope /private/scope.json --tunnel-credentials /private/credential.json [--check]' >&2; exit 1 ;;
  esac
done
[[ -n "$scope_file" && -n "$credential_file" ]] || { echo 'Provide both private input files.' >&2; exit 1; }
[[ "$(id -u)" != 0 ]] || { echo 'Run as your normal laptop user, not root.' >&2; exit 1; }
command -v python3 >/dev/null || { echo 'Python 3 is required.' >&2; exit 1; }
project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$project_root"
# Validate all inputs before replacing configuration or stopping any service.
python3 - "$scope_file" "$credential_file" <<'PY'
import json, os, stat, sys, uuid
from urllib.parse import urlsplit
from pathlib import Path
for name in sys.argv[1:]:
    p=Path(name); st=p.stat()
    if not stat.S_ISREG(st.st_mode) or st.st_uid!=os.getuid() or st.st_mode&0o077 or st.st_size>16384:
        raise SystemExit('Inputs must be private owner-only regular files under 16 KiB.')
scope=json.loads(Path(sys.argv[1]).read_text())
creds=json.loads(Path(sys.argv[2]).read_text())
try:
    uuid.UUID(creds['TunnelID'])
except (ValueError, KeyError, TypeError):
    raise SystemExit('Invalid dashboard tunnel ID.')
if not all(isinstance(creds.get(k),str) and creds[k] for k in ['AccountTag','TunnelSecret']):
    raise SystemExit('Invalid dashboard tunnel credentials.')
origin=urlsplit(scope.get('Origin',''))
if origin.scheme!='https' or not origin.hostname or origin.netloc!=origin.hostname or origin.path or origin.query or origin.fragment:
    raise SystemExit('Provide an exact HTTPS dashboard hostname.')
print('Private dashboard deployment inputs validated.')
PY
if [[ "$check_only" == 1 ]]; then
  go run ./cmd/configure-dashboard -check -config "$HOME/.config/projectx-bot/config.yaml" -state "$HOME/.local/share/projectx-bot" < "$scope_file"

  echo 'Validation complete. No configuration or services changed.'
  exit 0
fi
./scripts/install-user-service.sh --remote-dashboard "$scope_file"
# Install only this dedicated connector. Never touch the webhook tunnel.
python3 - "$credential_file" "$HOME/.config/projectx-bot" "$scope_file" <<'PY'
import json, os, sys, tempfile
from urllib.parse import urlsplit
from pathlib import Path
creds=json.loads(Path(sys.argv[1]).read_text()); directory=Path(sys.argv[2])
credentials=directory/'dashboard-tunnel-credentials.json'
config=directory/'dashboard-tunnel.yaml'
hostname=urlsplit(json.loads(Path(sys.argv[3]).read_text())['Origin']).hostname
content='tunnel: '+creds['TunnelID']+'\ncredentials-file: '+json.dumps(str(credentials))+'\ningress:\n  - hostname: '+hostname+'\n    service: http://127.0.0.1:8080\n  - service: http_status:404\n'
for destination,data in [(credentials,json.dumps(creds)+'\n'),(config,content)]:
    fd,name=tempfile.mkstemp(prefix='.dashboard-',dir=directory)
    try:
        with os.fdopen(fd,'w') as f:
            f.write(data);f.flush();os.fsync(f.fileno())
        os.replace(name,destination)
    finally:
        if os.path.exists(name):os.unlink(name)
PY
cloudflared --config "$HOME/.config/projectx-bot/dashboard-tunnel.yaml" tunnel ingress validate
install -m 644 deploy/systemd/projectx-dashboard-tunnel.service "$HOME/.config/systemd/user/projectx-dashboard-tunnel.service"
systemctl --user daemon-reload
systemctl --user enable projectx-dashboard-tunnel.service
systemctl --user restart projectx-dashboard-tunnel.service
systemctl --user is-active --quiet projectx-bot.service projectx-dashboard-tunnel.service
printf 'Installed. Open your configured dashboard hostname and sign in through Access, then your existing app login.\nTrading remains disarmed; review the dashboard before choosing Start.\n'

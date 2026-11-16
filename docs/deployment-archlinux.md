# Native Arch Linux deployment

Run only on the intended personal laptop, as your ordinary user, over a direct local internet connection. No Docker, remote host, proxy or WARP. Defaults are read-only; explicitly started automatic profiles can trade and monitor eligible accounts.

Install Go and OpenSSL if needed (`sudo pacman -S --needed go openssl gcc pkgconf sqlite`), then run the project-provided installer as your regular user:

```sh
./scripts/install-user-service.sh
cat ~/.local/share/projectx-bot/admin-password
```

Open http://127.0.0.1:8080 with username `local` and the generated password. Set ProjectX credentials and account settings in the browser. The credentials stay in the user's private data directory; the key is never returned in HTML. If the optional `~/.config/projectx-bot/environment` contains credential variables, they take precedence and the page shows that credentials are environment-managed. Remove those overrides and restart if you prefer browser management. Existing admins.json takes precedence over bootstrap/environment passwords.

```sh
systemctl --user status projectx-bot.service
journalctl --user -u projectx-bot.service -f
systemctl --user stop projectx-bot.service
systemctl --user start projectx-bot.service
systemctl --user disable projectx-bot.service
```

The installer preserves an existing config; the service can write both its private config and data directories. It builds and tests before stopping an existing instance, then replaces the executable and restarts. The state directory contains a plaintext owner-only credentials file, initial private bootstrap password, hashed administrator store, and automation policy/profile JSON. Manage administrator names/passwords and additional logins in Settings → Admin access. Once admins.json exists, deleting the bootstrap password file or changing BOT_DASHBOARD_TOKEN does not reset credentials. Use another administrator to recover access. Browser Basic Auth is cached by the browser; close the browser session after use on a shared desktop.

The implementation was verified using Go tests/vet/race checks, mocked gateways and local HTTP smoke tests, not an installed systemd activation or real ProjectX write. Browser visual inspection was unavailable. Private state includes execution.db (intents, target claims, global pause and locks), remote-alerts.db, signals.db, activity.db and automation-setups.json. Keep directory mode 0700 and files 0600. Backups must include consistent SQLite WAL state; stop services after independently verifying all accounts flat before copying state. Never delete execution state to clear a gate.

The user service starts with the user manager, normally at login. Do not enable lingering or deploy it to another device without considering the intended local-device constraints. The unit intentionally does not modify login, power or suspend policies.

A 2019 laptop can suspend on lid close or battery depletion; this prevents monitoring and future local order management. Keep it on reliable AC power during intended operation, inspect battery health, cooling and Wi-Fi reliability. Review desktop/logind suspend behavior yourself and plan a verified flatten before leaving or shutting down. Do not silently change system-wide power settings. Avoid unattended restarts during positions. Updates, network recovery, resume and battery loss require operational tests before future live execution. The one-minute discovery refresh is separate from immediate signal dispatch; the dashboard polls its local monitor endpoint every ten seconds while visible, without changing forms.

The SQLite inbox requires a CGO-enabled build with GCC, pkgconf and SQLite headers, and the SQLite shared library at runtime. Back up the inbox with SQLite backup tooling or stop the service before copying `signals.db`; do not remove it to clear a lockout or replay an old signal ID.

Optional incoming webhooks use a separate binary/service on loopback port 8090. The normal installer builds and starts its local managed service; receiving remains off until enabled in the dashboard. See [webhook receiver](webhook-receiver.md) for admin-managed activation, per-profile credentials and tunnel instructions. Never forward the admin port 8080.


The preferred setup now installs required Arch packages when missing (including cloudflared), starts both bot and managed receiver, and opens the desktop admin page. The receiver remains inbox-disabled until explicitly enabled in the UI. Tokens, template mappings and the owned Quick Tunnel are managed in that page. See [admin guide](admin-guide.md); earlier manual webhook environment-token steps are legacy alternatives, not required setup. Stop old terminal-launched receiver/tunnel instances once before migrating.

## Execution upgrade and laptop operation

The installer now refuses a saved active/unresolved execution. Verify flatness in TopstepX and do not submit while upgrading. Restart remains disarmed; persisted intents reconcile without replaying placement. A process lease prevents concurrent services using the same state directory. Keep one bot instance per account; do not use several state directories. The webhook service is denied access to execution state/configuration as well as credentials.

Before suspend, lid closure, battery exhaustion, OS updates or restart, flatten and independently verify the account. The app cannot act during laptop downtime; server-side Auto OCO is mandatory. No power-management setting is silently modified. Automatic monitoring uses shared market/user streams plus an adaptive REST backstop of max(5 seconds, 3 seconds × active accounts); manual tests retain a two-second backstop; idle trading does not busy-poll. See [manual execution](manual-execution.md) and [recovery](recovery-procedures.md).

## Named Cloudflare connector

Use `./scripts/install-user-service.sh --named-tunnel alerts.yelaco.trade` to provision a stable-hostname connector, webhook base URL and user service in one command. See [named tunnel setup and device migration](named-tunnel-setup.md) for the one-time Cloudflare route, secure token input, standalone-service conflicts, logs and migration boundaries. No trading permissions are restored by setup.

## Concurrent automation upgrade

Run the normal installer only with no unresolved intents and every account independently flat. It updates bot and receiver together, preserves configuration/credentials/tokens, and requires explicit Start again. A valid legacy single-profile setup migrates as configuration only; changed scope/calendar requires reconfirmation. No manual SQLite migration or Unix socket setup is needed. The main service creates the private inbox.sock after acquiring execution.lock; the receiver sends payload-free wake hints after commit. Both units allow AF_UNIX. The existing named Cloudflare connector does not change for a bot/receiver upgrade.

Use Execution runners for global and per-account controls and timing. Do not run two installations/state directories against the same account. At most one running profile owns an account. Stopping systemd does not establish flatness and broker brackets cannot replace a reliable running laptop. No power-management or system-wide service settings are changed by this implementation.

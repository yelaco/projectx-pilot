# One-command named-tunnel setup on Arch Linux

The installer can prepare the bot, receiver and a named Cloudflare connector on the intended local laptop:

```sh
./scripts/install-user-service.sh --named-tunnel alerts.yelaco.trade
```

It prompts for **only the tunnel token**, with hidden input. Do not paste the whole Cloudflare installation command. The token is saved atomically with mode 0600 in `~/.config/projectx-bot/cloudflared-token`; the service passes a file path, not the secret, on its command line. Rerunning the same command reuses the saved token. For unattended provisioning, supply an existing private file:

```sh
./scripts/install-user-service.sh --named-tunnel alerts.yelaco.trade --token-file /private/path/tunnel-token
```

The token file must contain only the token. Keep it outside the repository; never put the token itself on the command line or in a public backup.

## What is automated

- Existing installer checks/dependency installation, offline-capable Go tests/vet and native builds.
- Validate hostname/token input before stopping the bot.
- Existing durable execution checks refuse upgrades over unresolved trades.
- Obtain the same exclusive execution lease as the bot before updating its routing.
- Preserve existing profile delivery tokens and mappings; enable receiving, disable Quick Tunnel and set the permanent HTTPS base URL.
- Install/enable/start `projectx-bot`, `projectx-webhook`, and `projectx-tunnel` user services. The named connector restarts on failure and starts at user login.
- Trading is still read-only/disarmed on first installation; restart never restores execution permission.

The installer does not start a tunnel during development/tests. No Cloudflare or trading API requests are made by the configuration helper. Actual connectivity must be checked on the destination device.

## One-time Cloudflare setup

Domain registration and the named tunnel/hostname route remain Cloudflare account operations; this command does not buy domains or create/change cloud resources. Configure `alerts.yelaco.trade` to forward to **HTTP `127.0.0.1:8090`** with no public admin route by default. Optional full remote dashboard access uses a separate hostname and verified Cloudflare Access; see [remote dashboard setup](remote-dashboard.md). Use the token of that remotely managed tunnel. A hostname alone cannot identify or authorize the connector. Keep the Cloudflare-side route restricted to the notification receiver; the app cannot enforce a remote dashboard route configuration.

Cloudflare documents [`--token-file`](https://developers.cloudflare.com/tunnel/reference/run-parameters/#token-file) for remotely managed tunnels, requiring cloudflared 2025.4.0+. The installer checks flag support. Arch's package installs `/usr/bin/cloudflared`, used by the user unit. This is a notification-ingress connector, not WARP or a proxy for ProjectX requests. It does not establish provider approval for remote alerts.

## Moving machines

Use the personal laptop intended for execution, never a VPS. Pause trading, resolve/close positions and verify the old account is flat before moving. Stop the **old** receiver/connector/bot before starting the destination connector. Cloudflare replicas can route traffic to either device; do not run two execution installations behind one tunnel as a migration shortcut. The destination must reconcile fresh account/order/position state and be explicitly started for trading.

If you already installed the standalone system `cloudflared` service for this bot on the destination, stop/disable it explicitly before switching to the user-managed connector:

```sh
sudo systemctl disable --now cloudflared
```

Do this only for the connector dedicated to this application. The installer detects an active standard system connector and refuses to create a duplicate; it does not stop arbitrary services. Also stop any manually launched connector terminals.

The installer provisions services and routing, **not a trading-state migration**. On a fresh device configure ProjectX credentials, admin credentials, Trade Managers and automation profiles locally. It does not copy credentials, execution databases, approval state or old arming. Existing profiles on the destination are preserved; fresh profiles generate fresh delivery tokens, so update TradingView messages accordingly. Do not overwrite an unresolved execution ledger or restore an old backup to replay alerts.

## Status and logs

```sh
systemctl --user status projectx-bot projectx-webhook projectx-tunnel
journalctl --user -u projectx-tunnel -f
```

`active` means the connector process is running; confirm **Healthy** in Cloudflare and perform an inbox-only test before starting execution. The dashboard displays the permanent URL but does not monitor named-service connectivity; its Quick Tunnel status can still show stopped. Only Quick Tunnel process logs are integrated into the sanitized tunnel feed. Named-tunnel errors use journald at error level. The tunnel cannot receive requests while the laptop is suspended/offline. User services start at login; no power, linger or boot-login settings are silently changed.

To stop the named connector:

```sh
systemctl --user disable --now projectx-tunnel
```

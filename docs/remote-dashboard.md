# Remote dashboard on the trading laptop

The full dashboard can be published through Cloudflare Tunnel and Access. Broker requests still originate directly from the laptop. Configuration, databases, broker credentials, durable execution claims and arming remain on the laptop. An authenticated remote Start/Pause/Flatten/review invokes the existing dashboard handler and safeguards. Restart never starts trading. The laptop must be awake, online and running the bot and connector.

Remote access is disabled by default. The listener remains `127.0.0.1:8080`. Local HTTP access and saved logins continue working when Cloudflare is unavailable. Only the exact configured public HTTPS host is accepted remotely, from a loopback connector, with a verified Access RS256 application token (signature, issuer, audience, expiry and exact allowed email) and the existing app login. POST requests retain HTTPS origin checks, CSRF and existing action-specific password confirmations. Forwarding headers do not grant authority. No Access token, application password or API response body is logged. Key fetches are bounded, cached and rate limited, and failure denies remote access.

## Cloudflare setup

Use a dashboard hostname such as `pilot.yelaco.trade`, separate from the webhook hostname. Do not apply interactive Access login to TradingView's webhook receiver. Existing webhook routes/connectors must not be moved or replaced as part of dashboard setup.

1. Run `cloudflared tunnel login` and authorize the domain in the browser.
2. Create a dedicated dashboard tunnel with `cloudflared tunnel create projectx-dashboard`. Save its generated credential JSON privately; never copy secrets into this repository.
3. Create a scoped Cloudflare API token with account **Access: Apps and Policies — Edit** and **Access: Organizations, Identity Providers, and Groups — Edit**. Save only the token to `~/.cloudflared/access-api-token`, owned by your user with mode 600. The tunnel login certificate cannot manage Access apps.
4. Provision the exact-email Access application and write the local non-broker scope:

   ```sh
   python3 scripts/configure-cloudflare-access.py \
     --hostname pilot.yelaco.trade --email YOUR_EMAIL \
     --output "$HOME/.config/projectx-bot/dashboard-access.json"
   ```

   This reuses the account's Access organization, creating `yelaco-pilot.cloudflareaccess.com` only if none exists. It enables email one-time codes if needed. Email OTP is an identity gate, **not MFA**; stronger MFA can be enforced through your configured identity provider and Access policy. The script refuses to overwrite an existing app with a different policy. It does not publish a hostname or start services.

5. Apply the scope and install the tested bot using:

   ```sh
   ./scripts/install-user-service.sh \
     --remote-dashboard "$HOME/.config/projectx-bot/dashboard-access.json"
   ```

   The installer refuses upgrades with unresolved execution history, acquires the execution lease for the config edit, preserves existing broker credentials/logins/webhook routing, and restarts disarmed. Do not manually bypass the unresolved-execution refusal.

6. Write a private `~/.config/projectx-bot/dashboard-tunnel.yaml`, substituting the generated UUID and credential path:

   ```yaml
   tunnel: YOUR_DASHBOARD_TUNNEL_UUID
   credentials-file: /home/YOUR_USER/.cloudflared/YOUR_DASHBOARD_TUNNEL_UUID.json
   ingress:
     - hostname: pilot.yelaco.trade
       service: http://127.0.0.1:8080
     - service: http_status:404
   ```

   Preserve the public Host header; do not set `httpHostHeader` to the loopback address. The application's own JWT checks provide origin-side Access enforcement. Optional connector-side Access validation can be added as defense in depth with the same team and AUD.

7. Validate before publication:

   ```sh
   cloudflared --config "$HOME/.config/projectx-bot/dashboard-tunnel.yaml" tunnel ingress validate
   cloudflared tunnel route dns projectx-dashboard pilot.yelaco.trade
   install -m 644 deploy/systemd/projectx-dashboard-tunnel.service "$HOME/.config/systemd/user/"
   systemctl --user daemon-reload
   systemctl --user enable --now projectx-dashboard-tunnel.service
   ```

8. Verify public requests reach Access login, then sign in through Access and the app. Test a harmless configuration action before trading. Existing password-confirmed emergency controls retain their normal behavior; do not submit live trades or emergency flattening merely to test remote access.

## Operations

`systemctl --user status projectx-bot projectx-dashboard-tunnel` shows process state; it does not prove authenticated public connectivity. Review `journalctl --user -u projectx-dashboard-tunnel` for connector errors. The dashboard's existing tunnel widget describes the webhook Quick Tunnel, not this separate named dashboard connector.

To revoke remote access immediately, stop `projectx-dashboard-tunnel` or disable its Access app/policy. To disable application acceptance, stop the bot safely and remove the complete `remote_dashboard` section from local YAML. Existing positions and broker SL/TP stay untouched; tunnel failures never request Flatten or cancel protection. Remote controls are unavailable during internet/Cloudflare failure, so local recovery remains necessary.

The local YAML scope contains `remote_dashboard.origin`, `team_domain`, `audience`, and `emails`. Changing these requires a safe service restart; general dashboard edits preserve them. Access authorization and key caching never substitute for broker exposure, protection or risk reconciliation.

Authoritative behavior: [Cloudflare Tunnel](https://developers.cloudflare.com/tunnel/concepts/), [validate Access JWTs](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/validating-json/), [Access applications API](https://developers.cloudflare.com/api/resources/zero_trust/subresources/access/subresources/applications/methods/create/).

## Private deployment bundle

The same installer runs on any selected machine, with no machine-specific activation flag. Preparing this bundle does not start services on the preparation machine. Installation preserves the destination’s own configuration and broker credentials, and trading still requires an explicit authenticated Start.

The prepared private archive contains the updated source under `projectx-pilot/`, `dashboard-access.json` and `dashboard-tunnel-credentials.json`. It contains no broker credentials, administrator passwords, trading databases, Cloudflare management API token or account-wide login certificate. The tunnel credential is a secret: transfer the archive privately (for example via SSH or a trusted USB drive), keep it outside Git/cloud sharing, and remove transfer copies after installation.

On the trading laptop:

```sh
umask 077
mkdir -p "$HOME/projectx-dashboard-deploy"
tar -xzf /private/path/projectx-dashboard-deploy.tar.gz -C "$HOME/projectx-dashboard-deploy"
cd "$HOME/projectx-dashboard-deploy/projectx-pilot"
./scripts/install-remote-dashboard.sh \
  --access-scope ../dashboard-access.json \
  --tunnel-credentials ../dashboard-tunnel-credentials.json
```

The installer uses the laptop's existing `~/.config/projectx-bot` and `~/.local/share/projectx-bot`. It does not copy state from the preparation machine, recreate revoked logins, alter existing webhook routes, or start automation. Native binaries are rebuilt on the destination. `--check` validates private deployment inputs and Access scope without changing services or configuration.

Disable the dashboard connector on any previous destination before installing on another device. Never run the same dashboard tunnel credential on a preparation machine and the trading laptop at once.

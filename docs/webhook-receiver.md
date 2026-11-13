# Optional TradingView webhook receiver (inbox only)

This implements the inbound signal-delivery component, not automated trading. `cmd/webhook` runs separately from `cmd/bot`, imports no ProjectX client, reads no ProjectX credentials, and writes only unassigned signals to `remote-alerts.db`. Nothing watches these rows to place orders. The admin page remains on 127.0.0.1:8080 and must never be tunneled.

## Preferred managed workflow

Run `./scripts/install-user-service.sh`, then configure receiving, the managed Cloudflare test tunnel and per-profile mappings in the admin page. See [admin and monitoring guide](admin-guide.md). Creating a TradingView profile generates a dedicated token and `/tradingview/<id>` route. Managed receivers read ingress.json on each request, so disable/rotate/delete changes take effect without restarting. Profile strategy and symbol must match the route. Deliveries are bound to a profile identifier but never to an account or execution contract until local assignment. The monitoring endpoint never includes tokens.

The following manual environment-token workflow remains available for development/legacy setups, but it is no longer required or used by the installed managed service. The unqualified `/tradingview` endpoint is legacy-only; normal profiles use their generated URLs.

## Start on your personal laptop

Update/install the binaries using the usual project installer; it starts the managed local receiver with receiving disabled until configured in the UI. To use the legacy manual workflow instead, stop that managed receiver first:

```sh
./scripts/install-user-service.sh
```

Generate a separate ingress token and run the receiver in its own terminal:

```sh
export WEBHOOK_DELIVERY_TOKEN="$(openssl rand -hex 32)"
printf 'Dedicated inbox delivery token: %s\n' "$WEBHOOK_DELIVERY_TOKEN"
~/.local/bin/projectx-webhook -enabled \
  -listen 127.0.0.1:8090 \
  -state "$HOME/.local/share/projectx-bot"
```

The token is an inbox-only bearer capability, not your ProjectX key, account password, admin password or tunnel-provider token. Keep it private. The receiver never logs it, echoes it or persists it with the signal. The sender and HTTPS tunnel provider can see request bodies; disable request-body inspection/retention where available. Rotate the token if exposed. An absent/short token fails startup; omitting `-enabled` keeps the receiver off. For a repository-only run, build with `go build -buildvcs=false -o bin/webhook ./cmd/webhook` and use `./bin/webhook -enabled -state var` so the bot and receiver use the same private directory.

## Expose only port 8090

After installing/authenticating the chosen tunnel agent according to its official documentation, run **one** of these in another terminal:

```sh
# Cloudflare Quick Tunnel: test URL changes each run, no uptime guarantee.
cloudflared tunnel --url http://127.0.0.1:8090

# Or ngrok, with local request inspection disabled:
ngrok http http://127.0.0.1:8090 --inspect=false
```

Use the printed HTTPS URL plus `/tradingview`, e.g. `https://your-host.example/tradingview`. Do not append port 8090 to the public URL; the tunnel forwards HTTPS 443 to local HTTP 8090. Leave both processes running on your laptop. Neither command starts WARP or reroutes the bot's outgoing ProjectX requests. Do not add interactive email/login gates to the webhook path because TradingView cannot complete browser authentication. Never point either tunnel at port 8080.

Sources: https://developers.cloudflare.com/tunnel/get-started/quick-tunnels/ and https://ngrok.com/docs/gateway/agent/cli. Public deployment and tunnel setup were not performed in development. This workspace cannot bind listening sockets, so tests exercise HTTP handlers and the actual SQLite files without an external listener. This is a testing workflow, not production hosting verification.

## Configure a TradingView alert

Enable TradingView two-factor authentication. Set the webhook URL to the public HTTPS `/tradingview` URL. Use this JSON envelope, replacing the separate delivery token and all signal values:

```json
{
  "delivery_token": "YOUR_SEPARATE_INBOX_DELIVERY_TOKEN",
  "signal": {
    "id": "swift-mnq-unique-alert-001",
    "strategy": "swift-mnq-scalping",
    "symbol": "MNQ",
    "direction": "BUY",
    "entry": 20000,
    "stop_loss": 19995,
    "take_profit": 20010,
    "timestamp": "ACTUAL_ALERT_TIMESTAMP_IN_RFC3339"
  }
}
```

This is a schema example, not a directly working Swift Algo alert template. Prices are illustrative, not trade recommendations. The timestamp must be current and valid RFC3339; numeric entry/stop/target values must come from the real alert. Swift Algo's version-specific stop/target alert placeholders are **not verified**, so do not guess them. To produce a working Swift template, inspect the actual indicator's alert message fields. IDs must be unique per distinct event and stable on retries. No account IDs, credentials or execution instructions are accepted inside the signal. Local profile/contract assignment happens later.

TradingView requires public destination ports 80 or 443, currently does not support IPv6 webhook destinations, requires 2FA, and cancels a request after three seconds. It sends JSON content type for valid JSON and may fail to deliver alerts. Check its alert log's webhook status. Official source: https://www.tradingview.com/support/solutions/43000529348-how-to-configure-webhook-alerts/. The receiver uses a dedicated ingress capability for authentication; never include platform login credentials or passwords in alert messages.

## Local delivery self-test

While the receiver runs, use another terminal with the same `WEBHOOK_DELIVERY_TOKEN` exported. This generates a fresh schema test alert and sends it to local ingress without sending any trading request:

```sh
python - <<'PYTHON'
import datetime, json, os, uuid, urllib.request
payload = {
    "delivery_token": os.environ["WEBHOOK_DELIVERY_TOKEN"],
    "signal": {
        "id": "test-" + uuid.uuid4().hex,
        "strategy": "swift-mnq-scalping",
        "symbol": "MNQ", "direction": "BUY",
        "entry": 20000, "stop_loss": 19995, "take_profit": 20010,
        "timestamp": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "metadata": {"test": "true"}
    }
}
request = urllib.request.Request(
    "http://127.0.0.1:8090/tradingview",
    data=json.dumps(payload).encode(),
    headers={"Content-Type": "application/json"}, method="POST")
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
with opener.open(request, timeout=3) as response:
    print(response.status, response.read().decode())
PYTHON
```

This is fabricated test data, not an actionable trade. Reload the private dashboard to see it under TradingView deliveries, then discard it. Use the same test against the tunnel's HTTPS URL after local ingress succeeds; confirm the destination before sending and never substitute account credentials for the dedicated token.

## Responses and limits

- `202`: durably received, unassigned, execution false.
- `200`: exact duplicate already persisted; no new row.
- `400` / `415` / `422`: invalid envelope, JSON/schema, media type, price directions or stale/future signal.
- `401`: missing/wrong ingress token.
- `409`: same signal ID with different content; original is preserved.
- `429`: more than 60 requests per minute (global limit; forwarding headers are not trusted).
- `503`: receiver concurrency limit, disk error, busy DB or full queue; not acknowledged as accepted.

Only POST `/tradingview` exists. The service caps bodies at 8 KiB, inner signal JSON at 4 KiB, concurrent requests at four, pending deliveries at 1000, and incoming age at five minutes by default (`-max-age`, range 1s..15m). Database busy waits are 500ms. Disk stalls/tunnel latency can still exceed TradingView's deadline; reuse stable IDs to resolve ambiguous delivery by retry/deduplication. The database uses full synchronous transactions; acceptance occurs after commit. Application logs contain no request bodies or token. The global rate cap can cause rejection under unwanted traffic; add provider-side source restrictions using TradingView's current official IP list where appropriate. No source address or forwarded header alone is trusted as authentication.

## Local assignment and review

Reload the private admin page and open **TradingView deliveries**. An incoming signal is unassigned and has schema validation only, without authoritative tick/market/risk checks. Select a profile and an active contract and click **Import for local review**; this revalidates freshness/profile/metadata and binds the account using local configuration. You can instead discard a delivery. Then inspect it in **Signal inbox** and mark reviewed or reject. Neither action authorizes orders. Expired deliveries can be discarded, not refreshed by changing their original timestamp.

The remote and local inboxes are separate SQLite databases. If the process exits after the local insert but before marking a delivery imported, a repeated local import checks an exact matching signal/profile/account/contract before completing the status update. There is no automatic import worker. Do not delete either DB to bypass duplicate protection.

## Legacy manual user service notes

The shipped `projectx-webhook.service` is now managed automatically by setup. It reads only `ingress.json`, not an environment delivery token or ProjectX credentials. The bot unit wants the receiver unit, and the receiver is part of the bot service lifecycle. Do not replace that unit with the previous manual environment-token unit. Stop old manually launched processes before upgrading so port 8090 is available. The advanced `cmd/webhook -enabled` environment-token mode is still supported when run separately, but must not share a listening port with the managed receiver.

## Compliance scope

The official Topstep API policy was rechecked before implementing this component: https://help.topstep.com/en/articles/11187768-topstepx-api-access. It prohibits remote infrastructure and remote automated triggers in order flow. This receiver cannot transmit, modify or cancel orders. A separate local dispatcher can now execute a new delivery only when its exact automatic profile is configured and locally started. This is an isolated notification-recording component, not a claim that tunnel-delivered signals are approved for trading. The dispatcher is implemented but disabled by default; the operator must confirm provider permission for the intended workflow. The application no longer requires a support ticket/reference. Risk checks and explicit local Start remain prerequisites.

Validation errors now return the specific fixed validation message and log only that reason (never the alert or token) in the receiver terminal/journal. Signal IDs accept `!` so a TradingView continuous ticker such as `MNQ1!` may appear inside the ID; the signal symbol must still be the root `MNQ`. Avoid mixing a live `{{close}}` with fixed example stop/target prices: the relative directions must remain valid.

The success response is now `{"status":"received","execution_status":"queued"}` (HTTP 202). It confirms persistence only, not submission/fill. Review-only notifications still wait for human import/review; automatic-profile notifications are consumed by the local dispatcher with durable outcomes. [Activation and safeguards](automatic-execution.md).

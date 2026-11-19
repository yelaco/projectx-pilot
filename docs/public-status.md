# Public trading status

Implemented locally: a separate, unauthenticated, read-only mobile page, with account cards and account-detail trade history. Both listener and publication default to off. This is separate from the administrator dashboard and TradingView receiver. Public URLs do not require or grant Cloudflare Access/app authentication. This document does not assert that a hostname has been provisioned or that the feature is deployed.

## Publication

In the authenticated dashboard under Settings → Public trading status, select accounts, supply generic aliases, choose an exact HTTPS origin (for example `https://status.yelaco.trade`), and enable publication with the current administrator password. Existing CSRF and session requirements apply. Only accounts assigned to automatic profiles can be selected; removing an account from automatic configuration removes it from the public snapshot. Paused/waiting accounts remain visible when still configured for automation. Accounts discovered in the broker but not assigned to automatic profiles are never included. Copy/distribution routing and risk policies are unchanged.

Aliases and random stable public slugs are separate from broker account names and numeric IDs. Aliases should not contain identifying account information. Explicitly selected accounts publish account-wide realized P/L and trade history, including manual trading on those accounts. No open-position/order information, account balances, original balances, non-trading adjustments, broker IDs, raw errors, profiles, credentials or signal payloads are exported. Trade rows expose sanitized instrument codes, time, buy/sell side, quantity, execution price, gross realized P/L and known costs. Unknown contract formats display `Futures` rather than a raw identifier.

Publication settings are stored owner-only in `public-status.json` on the laptop. Settings changes clear the public financial cache and have no execution effects. Disabling publication immediately makes the public handler return 404. No configuration or authority can be supplied through a public request.

## P/L meaning

- **Today:** account-wide gross realized P/L minus all reported fees and commissions on nonvoided legs since the latest 17:00 America/Chicago. This is a display convention, not a trading calendar permission. At 17:00 a new reporting day begins, including weekends; the exit/holiday calendar is unaffected.
- **Total:** full lifetime realized net P/L, never simply the sum of whatever recent history happens to be available. The operator supplies the exact account's inception date, original balance and complete net non-trading adjustments (credits/deposits minus withdrawals/debits, excluding trade results and costs), and explicitly confirms that metadata with the password-protected save. A reset/replacement account needs its own correct metadata; names and nominal account tiers are not evidence.
- Total remains **Unavailable** unless inception/adjustments are confirmed, all nonvoided costs are known, and returned net trade history reconciles within $0.02 with the explicitly observed account balance minus original balance minus net adjustments. The public page labels this operator-confirmed and balance-reconciled, not independent broker certification of lifetime coverage. The Gateway documentation does not guarantee retention/completeness or supply account inception/initial balance. Missing history can still require operator review; offsetting omissions can mathematically reconcile. No unverified server lifetime field, inferred starting balance or account-size guess is used.
- Missing or null balance cannot establish reconciliation, including a zero-start funded account. A balance correction or a trade arriving between account/history observations can temporarily make Total unavailable; the next scheduled observation may reconcile it. No immediate retry or trading action follows.
- Missing costs produce unavailable net P/L, not zero costs. Explicitly returned empty history is distinct from unavailable history. Voided legs are excluded; duplicate/inconsistent history fails closed for reporting. Opening legs have null gross P/L; closing legs report gross realized P/L. Details show execution legs, not invented paired round trips. Unrealized P/L is excluded.

The account inception date also bounds available detail history. Without one, details cover Today only and Total stays unavailable. The newest 200 nonvoided legs are displayed, with a visible limit notice; totals use all returned valid legs. History is bounded to 10,000 legs and the existing 4 MiB response limit; requests beyond these bounds result in unavailable/stale data, never a silently partial public total. There is no new pagination/retention guarantee.

Official behavior references: [Gateway account search](https://gateway.docs.projectx.com/docs/api-reference/account/search-accounts/) and [Gateway trade search](https://gateway.docs.projectx.com/docs/api-reference/trade/trade-search/). These are the personal Gateway API, not the separate administrative S2F API.

## Isolation, refresh and failures

Only the laptop's existing shared authenticated client/governor performs scheduled read-only account/history requests. Disabled publication/listener never initializes a client for this feature. Financial collection runs approximately every five minutes, with a 45-second total context deadline and at most twenty selected accounts. It does not arm, place, cancel, modify or close anything. No live broker request is performed in HTTP handlers. Fleet state is copied into a bounded local cache approximately every fifteen seconds; visitors cannot initiate broker/Discord probes or SQLite fleet scans.

Financial snapshots are memory-only; restart displays unavailable until a successful collection, never restored freshness or arming. Transient failures retain last successful financial data and its unchanged timestamp, explicitly marked stale. Financial data older than ten minutes is stale; a previous session's Today value is withheld after 17:00 until refreshed. Automation status older than one minute displays unavailable. Browser fetching refreshes every minute while visible, with a ten-second timeout and a Refresh button. Browser failures retain the last snapshot with a failure banner. Offline laptops are not represented as current data.

The isolated listener has bounded HTTP timeouts/header size, exact configured Host matching, GET/HEAD-only routing, and no fallback to admin/webhook handlers. Only `/`, `/accounts/<opaque-slug>`, `/api/status`, `/status.css`, `/status.js`, `/install.js`, `/manifest.webmanifest` and the two public app icons exist. Forwarding headers are not authentication or hostname authority. Responses use no-store, no third-party assets, restrictive CSP, no-referrer and noindex headers. Noindex is a request to crawlers, not access control; data is genuinely public and can be copied. Listener bind/runtime failure reports generic unavailability without terminating the trading services.

## Deployment on the selected laptop

Prepare/review the change first. Use the guarded deployment procedure; never interrupt an active execution just to add a public page. Restart never retains Start intent.

1. In the laptop's private `~/.config/projectx-bot/config.yaml`, add:

   ```yaml
   public_status_listen: 127.0.0.1:8091
   ```

   The address must be loopback with a valid separate port, excluding the configured administrator port and receiver port 8090. No internet-facing bind is allowed. Safely restart after this listener configuration changes. The default empty value creates no listener.

2. Save publication settings and account metadata through the authenticated dashboard. No account is selected or published by default. Configuration refresh is picked up within about fifteen seconds; initial financial collection can take up to the bounded request deadline.

3. Add a **separate exact public hostname** to the laptop's existing Cloudflare tunnel with origin `http://127.0.0.1:8091`. For a locally managed tunnel, insert its ingress entry before the catch-all, validate with `cloudflared tunnel ingress validate`, and route DNS using `cloudflared tunnel route dns <tunnel-name-or-id> status.yelaco.trade`. These Cloudflare commands require the appropriate local management credentials. For a remotely managed tunnel, add its public hostname through the Cloudflare dashboard/API. Do not display or copy connector secrets into Git.

   ```yaml
   - hostname: status.yelaco.trade
     service: http://127.0.0.1:8091
   ```

   No `httpHostHeader` override: preserve the exact public Host. Do not point this hostname at the administrator port, bypass Access on the admin application, modify the TradingView hostname, or add an Access application to this public hostname. If an existing wildcard Access policy covers it, use a deliberately separate exact public hostname policy without weakening the administrator application's protection.

4. Verify without credentials that the page/API include only the selected aliases and public DTO, that `/settings` and mutation requests are refused, and that the administrator hostname still requires Cloudflare Access plus app authentication. There is no need to submit trades to verify publication.

Disable publication in Settings to revoke the public view immediately. Remove the dedicated public tunnel/DNS route and set the listener configuration empty on a later safe restart to remove the listener. Existing admin/webhook services and broker connectivity are unchanged.

## Validation

Automated tests use synthetic accounts/trades only. They cover net accounting including opening costs and voids, incomplete costs/history/identity, lifetime reconciliation, distinct inception/Today boundaries, DST, opaque identifiers, password/CSRF, publication revocation, credential changes, previous-session staleness, exact hosts, public route isolation, and browser stale/XSS behavior. No production credentials, broker requests, Discord messages or public publishing are needed by tests.

Cloudflare command/origin references: [locally managed configuration](https://developers.cloudflare.com/tunnel/features/locally-managed-tunnels/configuration-file/), [DNS routing](https://developers.cloudflare.com/tunnel/concepts/routing/) and [origin configuration](https://developers.cloudflare.com/tunnel/configuration/).

## Android home-screen installation

The public page includes a same-origin web app manifest, stable root app identity/start URL/scope, standalone display mode, theme colors, and 192/512 PNG icons with a mask-safe monogram. CSP allows only same-origin manifest/icons. The Install app button uses the browser's deferred installation prompt only after a click; otherwise it explains Chrome's Add to home screen → Install menu. In-app browsers may require opening the page directly in Chrome, and browser install promotion remains browser-controlled. The button is hidden in standalone mode or after installation.

There is no service worker and no offline cache of pages, account history or financial snapshots. Installation does not add background execution, a native Android widget or trading authority. The laptop/publication must be online to install and load current data; the existing stale indicators still apply. Disabled publication/wrong-host requests cannot load install assets. Check the actual device/browser installation UI after deployment; unit tests cannot establish that a particular Android browser will offer installation.

Installability follows [Chrome's published PWA criteria](https://web.dev/articles/install-criteria) and [manifest guidance](https://web.dev/articles/add-manifest).

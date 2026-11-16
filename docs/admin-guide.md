# One setup command, then the local admin page

From the project root on the personal Arch laptop, run:

```sh
./scripts/install-user-service.sh
```

The setup command checks/install missing Arch packages (sudo is used only for packages), runs tests/vet, builds both native binaries, preserves config/secrets/databases, installs and starts the bot and managed receiver user services, and opens the local page when a desktop opener is available. It prints the generated admin password and its private file location. This script has not been executed against the developer workspace's systemd manager.

**Upgrading from the old terminal workflow:** stop manually launched `projectx-webhook` and `cloudflared` terminals once before running setup. The installer stops/replaces its managed services but intentionally does not kill unrelated processes. Port 8090 must be available to the managed receiver. Normal subsequent upgrades need only the setup command.

Open http://127.0.0.1:8080 with username `local` and your admin password. Existing environment credential overrides remain supported; the page labels those as environment-managed.

## Dashboard organization

The dashboard now has distinct server-rendered pages rather than one scrolling form:

| Section | Contents |
| --- | --- |
| Overview | Short connection/account summary, alert pipeline, review counts and five recent events |
| Trade managers | Maximum planned risk per trade and account routing; saving switches legacy policies to per-trade-only |
| Automations | Profile directory and a separate creation page; each profile has General, Webhook & fields, and TradingView messages tabs |
| Inbox | Incoming alert assignment and local review as separate tabs; manual JSON import is collapsed until needed |
| Accounts | Account discovery/permissions, plus a Contract directory tab |
| Activity | Full recent operational feed, severity/search filters and snapshot export |
| Settings | ProjectX connection, Webhook & tunnel and Admin access as separate tabs |

Forms return to their own page after saving. Tokens appear only in the selected profile's webhook/messages tabs and start concealed in the credential field; overview/list/settings pages do not embed profile tokens. URLs are bookmarkable, navigation marks the active section, and the layout adapts to narrow screens. Monitoring continues across pages without overwriting form fields. The overview stays compact when the activity feed updates.

## Configure in the browser

1. Save the exact TopstepX login and API key in Settings. Refresh/discover accounts and choose your Practice account.
2. Create an automation profile with the desired strategy ID, account and instruments. Choose TradingView with local approval.
3. The profile immediately gets its own random delivery token, `/tradingview/<profile-id>` path and BUY/SELL template drafts. Existing TradingView profiles are migrated automatically on startup. Editing preserves tokens; rotation/deletion revokes old capabilities immediately without a receiver restart.
4. In Settings → Webhook & tunnel, click **Use Cloudflare preset**. This explicitly enables inbox-only receiving and the managed test tunnel in one action. Manual/external tunnel options are collapsed under Advanced. Receiving and public tunneling default to off. The receiver process is local and already supervised; enabling delivery is a configuration action, not order authorization.
5. The bot starts/restarts/stops its own cloudflared child and captures the public URL into the page. It never tunnels the admin port. No separate terminal command or manually exported delivery token is needed.
6. In the profile's **Webhook & fields** tab, configure **verified** entry, stop and target TradingView price placeholders. Entry defaults to `{{close}}`; stop and target start blank because Swift Algo's plot names/indexes are not known. Accepted expressions include `{{plot_0}}` through `{{plot_19}}` and named `{{plot("Stop Loss")}}` placeholders. Names must match the actual indicator. Do not guess plot indexes. No server-side code or expression arithmetic is executed.
7. Open the profile's **TradingView messages** tab and copy the generated webhook URL and BUY/SELL message into the corresponding TradingView alerts. Draft templates with missing mappings are clearly labeled and intentionally fail signal validation; there are no hardcoded example stops/targets mixed with live prices. TradingView 2FA is required.
8. Watch incoming deliveries in the dashboard. Locally assign a profile/contract, revalidate and review. Review-only deliveries have no automatic import/execution. Approved automatic profiles have a separate local dispatcher and show their durable outcome. Local manual tickets remain on Practice execution.

One mapping set currently supplies both BUY and SELL drafts for a profile. If the indicator exposes direction-specific or no stop/target plots, those fields need an indicator-specific adapter; do not assume that the generated generic template proves its fields are available. See TradingView's official placeholder reference: https://www.tradingview.com/support/solutions/43000531021-how-to-use-a-variable-value-in-alert/.

Quick Tunnels are for tests; the public URL changes when a new tunnel is created. The page updates automatically but TradingView retains its configured URL, so update existing alerts after a URL change. A stable external/named tunnel can instead use the Public HTTPS base URL field; named-tunnel service provisioning is available through the [installer option](named-tunnel-setup.md), while the dashboard does not manage its lifecycle. The managed tunnel uses only the fixed command `cloudflared tunnel --url http://127.0.0.1:8090`, with no shell or commands from form data. It does not enable WARP or modify outgoing ProjectX networking.

## Monitor and logging

The Overview and Activity pages update every ten seconds while the browser tab is visible, without reloading or overwriting configuration forms. Settings → Webhook & tunnel additionally has a dedicated running-log panel updated every three seconds, filtered independently to webhook/tunnel events. It shows:

- Last successful REST account snapshot, sanitized connection errors and service uptime.
- Whether the local receiver answers its loopback health endpoint. Listening does not mean public reachability or receiving enabled.
- Tunnel state (stopped, starting, connecting, running, disconnected, unavailable/failed) and sanitized failures.
- Persistent counts of unassigned deliveries and pending local reviews.
- Recent sanitized activity: snapshot successes/failures, administrative requests, tunnel state changes, received alerts, authentication and signal validation failures.

Filter by level or message text, or export the authenticated monitor JSON snapshot. The snapshot contains account data and sanitized activity but no API keys, admin passwords or delivery tokens. It is still private account information. Configuration tokens/templates are available only on the authenticated admin page and are never included in the monitoring endpoint.

The monitor stores up to 2000 activity events per database and displays the latest 100 merged service/receiver events. Events survive restarts. Signal payloads, tokens and raw cloudflared logs are not recorded in the activity feed. Receiver error reasons are fixed validation strings. Console service logs continue in journald; the dashboard is a sanitized operational feed, not an unrestricted journal viewer. Disk problems are shown as monitoring/storage errors rather than assumed healthy.

Practice execution separately monitors bot-managed working orders, positions, fills and protection after explicit setup/arming. Its quote subscription and risk checks are independent of account discovery. Account permissions default to denied. `canTrade` or a connected snapshot alone cannot authorize a trade. See [manual execution](manual-execution.md).

## Stop or disable

Use the receiving checkbox to deny webhook routes immediately. Unchecking Quick Tunnel stops the child the bot owns. Configuration and history remain. Stopping the bot user service stops its owned tunnel and the associated receiver unit. Power/suspend/network policy is unchanged; a sleeping laptop cannot receive alerts. This setup records notifications only and does not establish Topstep approval for connecting remote alerts to orders.


## Webhook & tunnel preset and running log

Use **Settings → Webhook & tunnel → Use Cloudflare preset** for the default testing setup. It enables receiving, selects the owned Quick Tunnel, clears any stale external origin on a mode switch, and wakes the tunnel controller. Applying the same preset again preserves tokens and mappings. The detected address fills in automatically; full profile URLs remain in each profile's TradingView messages tab. A disable button stops receiving and the owned tunnel without removing profiles or history. No preset can change trading mode or account grants.

The same page shows a persistent **Webhook & tunnel running log**, updated every three seconds. Filter webhook/tunnel components, search, or pause the log view; pausing does not stop the services. Events include accepted deliveries, exact duplicates, HTTP rejection status, fixed validation reasons, tunnel states and sanitized DNS/timeout/local-receiver errors. Its latest 100 component-specific events are selected independently from general admin activity, so busy settings activity does not displace connection diagnostics. Repeated identical tunnel warnings are throttled to one per five seconds. Raw stdout/stderr, request/response bodies, URLs containing credentials, and signal payloads are not copied into the log. Unknown raw tunnel errors produce a generic diagnostic rather than exposing process data.


## Administrator credentials

Open **Settings → Admin access** to rename your administrator login, change its password, create additional logins, or revoke one. All logins currently have full local administrator privileges; there are no viewer roles. Each management action requires the acting administrator's current password and the normal same-origin/CSRF authorization. New passwords require 12–72 bytes and confirmation. The last administrator cannot be removed; up to 20 logins are supported.

Existing installations migrate the current `local` bootstrap login to the private `admins.json` store. Passwords are stored with salted bcrypt hashes, not plaintext. The generated onboarding password file is retained only while the bootstrap login remains unchanged; it is removed after that login is renamed/reset/revoked. Browser Basic Auth caches logins: after changing your own username or password, sign in again with the replacement. Other logins remain independent. Revocation applies to subsequent requests; an already authorized in-flight request may finish.

Once `admins.json` exists, `BOT_DASHBOARD_TOKEN` is **not** an override or recovery backdoor; changing an environment variable or removing `admin-password` cannot restore an old login. Use another administrator to reset forgotten credentials. No unauthenticated password reset endpoint exists. Keep the private credential store backed up. Login checking has bounded concurrent/uncached bcrypt work and a short successful-verification cache; credential changes clear cached authentication. The implementation pins the locally available `golang.org/x/crypto` v0.39.0 bcrypt package to retain Go 1.23 support in this offline workspace. API reference: https://pkg.go.dev/golang.org/x/crypto@v0.39.0/bcrypt.

## Trade manager profiles

Use **Trade managers → New trade manager**, then select that policy in an automation's General tab. This separates the alert source (automation) from account routing and risk configuration (trade manager).

New/saved policies store planned risk per trade plus explicit single/multiple-account routing. Per-trade-only policies intentionally have no daily loss, trade-count, MLL or aggregate portfolio cap. Existing legacy policies retain their limits until explicitly saved. See [risk management](risk-management.md).

Automatic profiles can execute every selected target independently, up to 20 accounts per manager. Each running account belongs to one profile; distinct profiles run concurrently on distinct accounts. Multi-account policies require per-trade-only risk. Saving a referenced policy pauses affected profiles and requires confirmation of their changed setup; management of existing exposure retains its captured policy. A referenced policy cannot be deleted until reassigned.

Use **Execution runners** for global Pause/Resume and confirmed flatten-all, per-account status/control, shared user-stream health and per-target acceptance timing. Global Resume never starts profiles. Profile Start uses its own saved setup; Practice execution is only for manual API tickets. See [automatic execution](automatic-execution.md) for the full workflow and visible limits.

## Direct webhook addresses

The complete generated URL is visible on the automation list and at the top of every TradingView profile tab, with a Copy button. It is derived from the current public host and that profile's ID and updates automatically when the host changes. No URL is fabricated if the tunnel host is unavailable; the field shows a waiting state. Tokens remain separate from the URL and are not exposed on the automation list.

## Manual execution test

Use **Practice execution** in the sidebar for Market Order / Limit Order buttons, exact contract quantity and stop/target ticks. Preview calculates risk without trading; password-confirmed approval is a separate action. Fresh account state and quotes, a confirmed fixed floor only for legacy policies, Auto OCO and verified ordinary-session hours are mandatory. [Full setup and failure procedures](manual-execution.md).

## Shared or separate Buy/Sell field mappings

In **Automations → your profile → Webhook & fields**, choose **Shared placeholders for Buy and Sell** or **Separate Buy and Sell placeholders**. Shared uses the same entry, stop-loss and take-profit expressions in both generated messages. Separate gives each direction its own three fields; entry can still use the same placeholder in both if desired.

Existing profiles remain shared by default. Switching modes retains previously saved sets, and only the selected mode's fields are saved. On the first switch to separate, the form starts from the existing shared values so they can be edited. Each active entry expression is required. Unknown stop/target expressions can remain empty, but that direction's message stays a marked draft; there is no fallback from a missing separate mapping to the shared value.

After saving, open **TradingView messages** and copy the updated BUY and SELL drafts into the corresponding TradingView alerts. Previously pasted TradingView messages do not update automatically. Mapping edits preserve the delivery token unless rotation is explicitly selected. These settings generate alert messages; they do not enable webhook execution.

## Practice execution: invalid account ID when saving

An earlier dashboard script accidentally disabled the execution account selector whenever a Trade Manager was selected. Disabled controls are not submitted by browsers, resulting in an empty account ID. This was a local form bug, not a ProjectX account rejection. The fix scopes derived-account behavior to automation-profile forms; the Practice execution selector remains enabled and required. Update the installed service with `./scripts/install-user-service.sh`, then reload the page.

## Automatic signal execution and easier arming

Use the profile's **Automatic execution** tab to configure the pinned expiry, Market/Limit entry and review/automatic handling. After saving Trading setup, use the single **Start automation** control in that same profile tab. Enablement, account eligibility, settings, start/pause, emergency flatten and monitoring are all available there; no Practice execution visit is needed. New matching alerts execute without per-alert clicks; review-only profiles keep the original inbox. The receiver does not expose an execution HTTP endpoint. [Complete workflow](automatic-execution.md).

Normal-hours preset fills Chicago session times from a date picker. Custom early-close windows use date/time pickers with server-side DST handling. Selected account name is displayed automatically; a previously verified floor is remembered as an editable default, with fresh confirmation still required. Manual arming lasts up to one hour and manual previews last two minutes. Continuous automatic Start refreshes execution permission only in verified windows after fresh account/risk reconciliation. No preset automatically verifies a holiday or enables trading.

## Continuous Start/Pause interface

Automatic execution now shows a compact Start/Pause control with status and outcomes. Signal routing, Trading setup and Emergency controls are collapsed. Trading setup combines account eligibility, cost/quantity settings and one Practice/calendar confirmation; it is saved once with password confirmation. Start uses the already authenticated local login and CSRF protection, with no daily form.

A running choice waits through closures/daily risk lockouts and can obtain fresh permission for the next verified session automatically. Trade Manager schedules define start/last-entry/exit reminder (legacy `flatten` field) in Chicago time, capped by reviewed 2026 holiday cutoffs with ordinary Chicago accounting. Unknown data/hours never authorize a trade. Explicit Pause/configuration changes and critical execution faults stop the runner. A restart still starts paused; use the single Start button after recovery. This replaces the former daily session-start workflow. [Current instructions](automatic-execution.md).

## Simplified Trade Managers

Open Trade managers → your profile, set **Maximum planned risk per trade**, then Save. Saving converts an existing legacy policy to per-trade-only and pauses trading. Return to the automation and Start after reviewing setup. A simplified policy requires no manual floor and does not calculate/enforce MLL, daily-loss or trade-count limits. Broker permissions, stop/target protection, cost-aware sizing, one active execution, session cutoff and duplicate/recovery checks remain. Historical lockouts are preserved.

## One saved public webhook hostname

The saved Public HTTPS base URL is the source for the overview, automation list, profile tabs, TradingView template URLs and live monitor endpoint map. Set it once in Settings → Webhook & tunnel (or use the named-tunnel installer). Each profile URL adds its own `/tradingview/<profile-id>` path; delivery tokens stay in messages, never URLs. Named-tunnel addresses survive restart. Existing profiles automatically use the saved hostname; a domain is never guessed from the incoming admin Host header or a Cloudflare token.

The overview and webhook settings offer Copy base URL; copy the full profile URL for TradingView. Permanent hostname and Temporary Quick Tunnel labels describe the address source, not external connectivity. When a permanent hostname is saved, the optional temporary Quick Tunnel preset is collapsed. Changing the saved base updates all views and live copy controls; TradingView's own alert configuration still needs updating when a URL changes. No configuration fields are overwritten by background monitor updates.

Practice execution resolves the saved Trade Manager before rendering session authorization. Per-trade-only managers hide both the fixed-floor input and its confirmation, including when an authorization error redisplays the form. Legacy managers still show these fields until saved in the simplified Trade Manager editor. Merely selecting an unsaved manager in the settings dropdown does not change the active saved policy; Save execution settings first.

## Webhook profiles and independent Practice API testing

New dashboard automation profiles have a fixed webhook signal source. Local-manual and future local-strategy source choices have been removed. Legacy non-webhook profiles can still be viewed/deleted; saving them explicitly converts them to webhook profiles, generates routing credentials and pauses entries. The local review page displays received webhook signals and stored history, with no manual JSON import form.

Practice execution requires no automation profile or webhook delivery. It is a local API order test on the saved Practice account/single-account Trade Manager and discovered active contract. Preview calculates risk without transmission; the separate password-confirmed approval sends a real Practice order. Read-only account/connection monitoring already checks API access without placing an order. Automated webhook trading is controlled separately by the profile’s Start/Pause controls.

Practice execution now labels persistent application locks as **Bot local safety lock**, separately from the provider account's trading permission. The narrowly scoped **Review local lock from the old OCO checker** panel appears only for the matching legacy lock and closed current-session executions. It requires password confirmation, historical proof and two fresh flat-account observations, records an audit repair, and leaves trading paused. See [recovery procedures](recovery-procedures.md#reviewing-the-known-one-way-oco-false-alarm). It cannot clear daily loss locks or unresolved order uncertainty.

## Operation days

Open **Operation days** to plan Saturday–Friday weeks. The general calendar is
shown first, followed by each Trade Manager. Lower calendars can add blocked
dates but cannot override a general or automatic FOMC block. Saving pauses entries;
monitoring and emergency management continue. See [operation days](operation-days.md)
for session-date semantics, persistence and news-source limitations.

## Access from another computer over SSH

Keep the bot's admin listener at 127.0.0.1:8080. On your browser computer, with
local port 8080 free and an authorized SSH login to the personal bot laptop:

```sh
ssh -N -o ExitOnForwardFailure=yes -L 127.0.0.1:8080:127.0.0.1:8080 your-user@bot-laptop
```

Open http://127.0.0.1:8080 on that computer and authenticate with the usual admin
credentials. Keep SSH running while using the page. Host/origin checks require
the matching port and address, so a different local port will be rejected. This
forwards admin access only; no change to the public webhook tunnel or outbound
ProjectX routing is required. Do not forward port 8080 through the public tunnel.
SSH server/network setup is not performed by the bot installer. Remote dashboard
access does not establish provider approval for an order-trigger workflow.

## Standalone operator guide

Open `/guide` on the local bot service, or use the **ⓘ Guide** button in the dashboard header to open it in a new tab. It has its own layout, quick start, detailed setup/operation sections, status explanations, and troubleshooting. It requires no login and contains no account data, credentials, profile state, or trading controls. Administrator pages and APIs still require authentication. The guide remains on the local dashboard port and must not be publicly tunneled.

## Instrument scope

Set instruments on each automation profile. There is no separate global list in Connection settings. Profile, strategy, pinned contract, authoritative metadata, risk and session checks still determine which alerts can execute. Creating or editing an unrelated profile does not require changing connection settings or stopping monitoring on another automatic account. Editing the running profile itself pauses its entries; existing trades retain their captured policy and continue monitoring.

Practice tickets list discovered active contracts with valid provider symbol metadata and still require PRAC eligibility, arming, exact-quantity risk checks, preview approval and protection. The retired YAML `allowed_instruments` field is accepted for compatibility, ignored at runtime and omitted when configuration is next saved. Connection/credential changes remain protected while accounts need monitoring. No symbol-name calendar gate remains. The selected manager window must fit the contract’s official hours; automatic holiday coverage remains limited to reviewed years.

## Trade Manager schedules

Each Trade Manager editor has start, last-entry and flatten time inputs in America/Chicago. Defaults are 17:00, 15:00 and 15:05. Starts at/after 17:00 refer to the preceding evening; earlier starts refer to the trading date. One window applies per weekday. Use appropriate earlier windows for products with earlier closes or pauses. Last entry must precede flatten by at least two minutes and flatten must be by 15:08 Chicago. Known holiday cutoffs only narrow these times. Unknown holiday years cannot authorize automatic trading.

Changing a manager pauses its assigned profiles and requires saving their updated trading-setup confirmation before restarting. Unrelated managers continue independently. Existing trades retain their captured window; editing a schedule does not change the management deadline of an already placed order. Legacy managers without schedule fields keep the original default. Old clients omitting schedule fields cannot erase a previously saved custom schedule.

## Position control and Discord attention reports

Only your explicit password-confirmed emergency Flatten action may request closing exposure. Local technical faults, risk thresholds and exit reminders pause/report without changing SL/TP or closing positions. The last verified position persists across restart as historical evidence, not fresh execution permission. Legacy risk settings do not guarantee floor protection or enforce automatic exits. Configure optional Discord attention reports in Connection settings using a private incoming webhook and current admin password. Saving notifications never changes trading authorization. [Behavior and delivery limits](attention-reports.md).

Daily trading reports are configured separately in Connection settings, with their own Discord webhook and enable toggle. The confirmed schedule is 16:15 Chicago; the window is previous 17:00 through report-date 16:00. One account-wide report per configured account/day includes realized P&L, costs and closing-leg counts, excludes unrealized P&L and preserves unavailable values. Delivery attempts are durably deduplicated across restart. See [reporting scope and limitations](attention-reports.md#separate-daily-trading-reports).

Account lock controls now classify all fixed management/protection lock reasons. Reviewable technical, exposure, accounting and timing faults offer a separate password-confirmed review that leaves open positions/orders untouched. Resolved submission/cancellation/emergency faults require terminal history before review; uncertain executions and risk locks cannot be waived. Ownership conflicts, global pause, process leases and session authorization remain separate gates. [Exact rules and persistence](recovery-procedures.md#shared-account-lock-review).

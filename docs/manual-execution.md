# Manual Practice execution

This workflow transmits actual orders to the ProjectX API on a confirmed Practice account. It is not application paper mode. No trading request was sent during development; unit/in-memory integration tests cannot establish that your subscription, platform bracket settings or account support these operations.

## Update and open

Ensure the account is flat and avoid submitting anything while upgrading. From this checkout run:

```sh
./scripts/install-user-service.sh
```

The installer runs tests/vet, builds and restarts the native services while preserving configuration. It refuses an upgrade when the saved execution ledger contains an active or unresolved intent. No systemd or external account changes were made in this development workspace. Open http://127.0.0.1:8080/execution using your existing administrator login.

## Configure once

1. On **Accounts**, grant execution eligibility for the intended Practice account. The user's screenshot identified `26567907`, `PRAC-V2-633355-95027627`; this is an observed name/ID, not an API guarantee of Practice classification. Never select the evaluation or Lab account for this workflow.
2. Create a **single-account Trade Manager** targeting that account. Configure planned dollar risk. Legacy managers retain session loss/trade/open-risk limits until saved in the simplified editor. Start with the desired small maximum contract quantity; these settings are enforced together.
3. No automation profile is needed. Practice execution is a direct API order test on the saved Practice account and single-account Trade Manager. Select a discovered active contract; no global instrument allowlist is required. Pause automatic trading on this account and arm the test session before previewing. Other accounts may continue automatically.
4. In TopstepX **Settings → Risk Settings**, enable **Auto OCO Brackets** for the account. The default Position Brackets mode rejects API bracket objects. Do not omit protection to work around rejection.
5. On **Practice execution**, select the same account and Trade Manager. Set estimated **round-trip commissions plus fees per contract**, slippage reserve in ticks, maximum contracts, and maximum signal-to-market deviation. The example costs are placeholders, not verified account fees. Enter your current administrator password and explicitly enable the capability.

Saving settings does not arm or submit. Both `mode: manual-approval` in YAML and enabled private `execution.json` are required; changing only one is insufficient. Fresh account grants are a separate gate.

## Verify and arm each time

The selected PRAC account name is shown automatically. With a simplified Trade Manager, no floor field is shown or required; the bot enforces planned per-trade risk and does not calculate/enforce MLL or cumulative daily loss. Existing legacy policies still show their verified fixed-floor field. Save the manager in its simplified editor to switch policies; saving pauses execution.

Choose the normal-hours preset and trading date, or use custom Chicago date/time pickers. RFC3339 remains supported for older local forms; timezone offsets are now handled by the server. Dates are interpreted using America/Chicago regardless of desktop timezone. For a normal winter session, `2026-11-13T15:05:00-06:00` illustrates the syntax only; use the actual current date and verified instrument hours. Flatten must be no later than 15:08 Chicago and at least two minutes after the final entry. Early closes require earlier values, including Topstep's holiday closure margins. The full risk-accounting session starts the previous day at 17:00 Chicago. **Merged holiday accounting sessions are unsupported**. The operator must verify the official calendar and ordinary session boundary; no calendar is inferred from these example values.

Confirm Practice classification, Auto OCO and the verified ordinary session schedule (plus a fixed floor only for legacy policies). Arming retrieves fresh account/positions/orders and requires the account to be flat with no working orders. Manual arming lasts up to one hour, clipped to the last-entry cutoff. Do not place manual platform orders or run another bot/copy-trading follower on the account while armed. Start only one bot/state directory for an account.

## Test Market and Limit buttons

1. Select the actual active contract expiry, **Buy/Sell**, **Contracts**, **stop-loss ticks**, and **take-profit ticks**. Enter positive tick distances (for example 40 stop / 80 target). The bot converts them to signed API offsets: BUY -40/+80, SELL +40/-80. Direction sets which side of the actual fill receives the stop and target. Tick size/value come from ProjectX metadata.
2. For Limit Order, enter a tick-aligned limit price near the current market. For Market Order, the limit field is ignored; a fresh streamed ProjectX quote supplies the reference price.
3. Click **Market Order · preview** or **Limit Order · preview**. The first request starts the quote subscription; if still connecting, wait a few seconds and retry. Inputs are retained on error. Neither button places an order.
4. Review account, contract, direction, order type, exact quantity, costs, planned risk and bracket distances. Requested quantity is rejected if too large; it is never silently resized. Preview expires after two minutes; all current risk/quote/session checks run again on submit.
5. Enter your password and click **Approve & submit to ProjectX Practice**. All risk/account/session/quote checks run again. Changed budgets/metadata require a new preview.
6. Inspect the live execution summary and recorded observations. `accepted` means acknowledgement only; `partial`/`protected` require observed linked stop and target coverage. Current working orders and position average prices are recorded; refresh to view full detail, while the summary updates every three seconds.

Planned risk includes the stop distance, estimated round-trip costs and slippage reserve. Market fills and stop gaps can exceed estimates. Brackets are relative to actual fills, so absolute stop/target levels may differ from the reference/limit calculation. A successful manual test validates this engine on that account. [Automatic TradingView routing](automatic-execution.md) uses the same engine with a separately recorded profile configuration and explicit local Start.

## Pause, emergency and failures

Pause prevents new submissions, invalidates previews and asks the monitor to cancel any working entry remainder. Position monitoring continues. Policy, eligibility and connection-setting changes disarm entries. Active intents freeze their risk configuration; connection/credential/execution configuration changes are refused until they finish.

Emergency flatten applies only to the bot-managed pinned account. The fail-safe also runs on missing/inconsistent protection, stale quotes while exposed, verified loss-buffer violation or the configured flatten deadline. It persists a session lockout and a one-attempt emergency latch before mutations, cancels and verifies entry-parent cancellation, closes current positions, then cancels orphan orders after observing flatness. It never blindly repeats an ambiguous close. Failure at any step requires immediate independent action in TopstepX; a network or laptop failure can prevent completion.

A timeout, malformed response or upstream write failure leaves a durable uncertain intent. Never resubmit that signal, delete the database or restart to bypass it. Reconciliation searches by unique `customTag`/order ID, retrieves current positions/orders and never replays placement. An unresolved missing order keeps the account blocked. Confirmed terminal rejected/cancelled orders require repeated flat observations; filled-and-closed trades require closing-fill evidence. Ambiguous-submission and protective-failure session locks remain even after flatness. Contact the provider if a submission cannot be resolved; there is no speculative “clear uncertainty” button.

Monitoring uses a bounded REST reconciliation loop every two seconds while an execution is active. It is not instantaneous protection. Server-side Auto OCO is mandatory. The market quote subset uses SignalR JSON over maintained Gorilla WebSocket with reconnect/backoff, subscription restoration, bounded frames, refreshed tokens and stale-data checks. Shared user-hub streaming and multi-account automatic execution are implemented separately. General unmanaged account-wide position management, order modification, trailing stops, paper execution and local strategy generation remain unsupported. Approved automatic TradingView routing is described separately.

Automatic setup and arming now live entirely in **Automations → profile → Automatic execution**. This page is for manual test tickets; its arm form always requests manual mode. Automatic sessions use the shared engine but have separate profile controls.

The Practice API test has no automation selector and works with zero automation profiles. Contracts come from active discovery with valid provider symbol metadata; the selected account and Trade Manager remain server-pinned. Tests create an internal ephemeral signal with strategy `practice-api-test` through the existing engine, not a stored automation profile. Exact quantity must fit risk, preview does not submit, and approval revalidates before durable placement. Incoming webhook handling is separate and unchanged by API testing. The dashboard no longer creates local-manual/local-strategy profiles or accepts manual JSON signal imports; existing historical records remain available. Retired import/JSON-preview POST routes cannot initiate execution.

## Optional maximum price deviation

Check **Disable maximum price deviation** in Practice execution → Execution settings to remove the signal/entry-to-market distance check for Market and Limit orders. Save (which pauses execution), then arm the test session again. The automation profile offers the same toggle in Trading setup; save and explicitly Start again for webhook execution.

The setting is persisted as `disable_price_deviation` in private execution configuration and captured in durable intents. Default/legacy configurations omit it and keep deviation enforcement enabled. `max_deviation_ticks: 0` still means zero permitted distance, not disabled. A disabled check is honored both at preview and submission; limit prices are not silently moved. Fresh quotes, signal validity/tick alignment, fees/slippage-aware risk sizing, quantity caps, account permissions, session restrictions, durable idempotency and protective orders remain required. Market entry is no longer bounded by the original signal price; brackets remain relative to actual fills, and estimated risk is not a guaranteed loss ceiling.

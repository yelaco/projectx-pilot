# TradingView alerts: local import and review

The main application has a **local signal inbox**, and its admin listener does not expose a public webhook. TradingView cannot deliver alerts to 127.0.0.1 on your laptop from its servers. An optional separate inbox-only receiver is now implemented; see [webhook setup](webhook-receiver.md). It is disabled by default and must be explicitly started and tunneled. No remote execution bridge is provided.

## Test the inbox

1. Create a profile assigned to your Practice account, with strategy `swift-mnq-scalping`, instrument `MNQ`, source **TradingView with local approval (planned)**.
2. Refresh ProjectX so account and contract metadata are current. Account execution permission may stay denied; imports and review cannot submit orders.
3. Open **Signal inbox**, select the profile and the actual active MNQ expiry from the contract list.
4. Paste an alert JSON object, then choose **Validate & queue**.
5. Inspect the saved prices/account/contract and choose **Mark reviewed (no trade)** or **Reject**.

Example **schema only** (replace the timestamp with the actual current alert time and prices with the actual alert levels):

```json
{
  "id": "swift-mnq-unique-alert-001",
  "strategy": "swift-mnq-scalping",
  "symbol": "MNQ",
  "direction": "BUY",
  "entry": 20000,
  "stop_loss": 19995,
  "take_profit": 20010,
  "timestamp": "2026-11-13T08:00:00Z",
  "metadata": {"source": "tradingview", "indicator": "swift-algo"}
}
```

These illustrative prices are not a suggested trade. Reusing this old example timestamp will be rejected. Each distinct alert needs a unique stable ID; retries must preserve it. Prices are numbers, not strings. `timestamp` is RFC3339 with a timezone. `direction` is BUY or SELL. Optional metadata is a small string-to-string map. Unknown top-level keys, duplicate top-level keys and account overrides are rejected. The locally selected profile chooses the account, and the locally selected contract is bound to the stored record; alerts cannot change either.

Validation covers schema, ID, matching strategy/instruments/profile, an account present in the current snapshot, a matching active contract and API tick size, positive prices, stop/target direction, freshness (default `signal_max_age: 5m`, maximum 15m), future clock tolerance (30 seconds), and durable ID deduplication. Refresh interval still governs account/metadata snapshots, not scalping execution. Continuous chart symbols are not tradable contract IDs and are not automatically rolled/mapped. Imports require metadata from a recent successful snapshot.

## What review means

States are `pending_review`, `reviewed`, `rejected`. Review records human inspection only. It does **not** approve an order, bypass `canTrade`, arm the bot or reserve risk. Expired imported signals stay as history. A future execution engine must revalidate everything and require a separate fresh authorization; it must never treat historical `reviewed` rows as an order queue.

The separate local manual Practice ticket now uses fresh quotes, risk sizing, session gates and bracket verification. Review-only and manually imported historical alerts do not enter that path. Configured, locally started automatic profiles can route new deliveries through the shared engine. Reviewing an inbox record still cannot submit an order. A successful manual test does not enable or certify remote-trigger routing; the local Start choice remains required.

The private state directory contains `signals.db`. Signal ID uniqueness survives restarts; signal records and audit events are committed together. Pending imports are capped at 1000; review/rejection frees pending capacity while retaining ID history. The UI displays up to 100 records, pending first. There is no automatic deletion of IDs. Arch builds now need CGO, GCC, pkgconf and SQLite development headers; the implementation uses a narrow binding to the system SQLite library because no Go SQLite driver was available offline. Prepared parameters and copied string bindings follow https://www.sqlite.org/c3ref/bind_blob.html; connection behavior follows https://www.sqlite.org/c3ref/open.html.

## Swift Algo and future delivery

Swift Algo's alert field availability must be verified for your indicator/version before creating a TradingView message template. We have not verified placeholders for its stop-loss/target values, so this project does not invent them. Swift Algo's official alert guide: https://swiftalgo.gitbook.io/guides/guides/how-to-set-up-alerts. Until an actual alert schema is confirmed, enter the actual signal values locally for inbox testing.

Future network delivery must remain a separate, disabled component subject to Topstep policy review. Local execution does not itself establish cloud-trigger compliance. TradingView defaults to local review; automatic execution requires configured profile/account scope and local Start. The app no longer records a provider ticket; confirming permission remains the operator’s responsibility. The separate receiver records deliveries only; it cannot initiate orders.

Per-profile **Webhook & fields** now supports shared mappings or independent Buy/Sell entry, stop-loss and take-profit placeholders. Legacy profiles remain shared. Generated messages use the selected direction's exact mappings, with independent readiness warnings for incomplete drafts. Use the same entry placeholder in both separate sets when only stop/target plots differ. Save and recopy both messages into the appropriate TradingView alerts; existing alert message text is not updated remotely. See [mapping setup](admin-guide.md#shared-or-separate-buysell-field-mappings).

[Automatic TradingView execution](automatic-execution.md) is now implemented with exact-profile routing, permanent delivery claims, real ProjectX write capability, fill/protection monitoring and crash reconciliation. It remains disabled by default and was tested only against mocks; no live account activation was performed.

## Trade-specific TP/SL exit alerts (implemented)

The same profile webhook accepts `action: "tp"` or `action: "sl"`.
Omitting action (or using `entry`) retains the existing entry behavior.
An exit carries its own unique `id` and a `trade_id` equal to the exact original
entry signal `id`. Direction is the original BUY/SELL direction, not an opposite
entry instruction. Account and contract come from the locally running profile;
an alert cannot select them. Exit alerts omit all three price fields and metadata.

```json
{
  "delivery_token": "YOUR_PROFILE_DELIVERY_TOKEN",
  "signal": {
    "id": "unique-exit-event-1",
    "action": "tp",
    "trade_id": "exact-original-entry-signal-id",
    "strategy": "YOUR_STRATEGY",
    "symbol": "MNQ",
    "direction": "BUY",
    "timestamp": "{{timenow}}"
  }
}
```

Profile → Messages includes BUY/SELL TP/SL drafts. The trade reference is an
intentionally invalid placeholder until replaced. Your indicator/alert generator
must retain the original entry ID (including its original bar timestamp when
using the generated entry templates). Using `{{time}}` on the later exit bar does
not recover the original entry ID. There is no unsafe "latest trade" fallback.
Fixed TradingView messages cannot remember earlier alerts on their own. If your
indicator cannot provide this reference, these exit drafts cannot be used safely
without modifying its alert generation. No Pine/Swift indicator was changed here.

A new exit needs the existing explicit local Start, exact profile/account/contract,
fresh timestamp, original strategy/symbol/direction, and receipt after arming.
The receiver persists it and the dispatcher durably claims it once. Review-only,
paused/pre-arming, unknown-trade and expired deliveries cannot execute later.
An exit referencing an already terminal trade or a latched exit is a no-op, even
when another trade is active. Exits arriving before their entry has a durable
managed intent are rejected; no future cancellation is scheduled.

For a managed pending entry, cancel it. For a partial/full fill, cancel the entry
remainder first, reconcile again for a racing fill, close the contract through
ProjectX, verify flatness and remove only the referenced orphan brackets. OCO
children stay in place while exposed. An ordinary exit refuses unrelated account
orders or unexplained position size/direction. Do not manually add exposure to a
bot-managed account: the API exposes net contract positions, not strategy lots.
A successful exit is marked flattening then closed after two flat observations
at least three seconds apart; it does not intentionally pause the running profile.
The first trigger and observations remain in the execution record. Each exit
latches the mutation attempt before network writes. An ambiguous cancellation,
close, scope mismatch after mutation, or failed verification blocks entries and
requires review; recovery never resends the exit. No opposite entry order is
submitted. TP and SL actions use the same close/cancel behavior; a market close
is not guaranteed at the signal's TP/SL price. Existing Auto OCO remains required.

Verified API: [Close Positions](https://gateway.docs.projectx.com/docs/api-reference/positions/close-positions/)
accepts accountId and contractId; existing cancellation and position/order search
endpoints are reused. Ownership/matching rules above are application safeguards,
not API strategy attribution. Tests use mocked HTTP receivers and gateways; no
new live exit request has been performed. Keep operator responsibility for the
exact remote-trigger workflow described in [compliance](topstep-compliance.md).

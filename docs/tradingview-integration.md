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

## Entry-only execution and fixed limit lifetime

TP/SL webhook actions, trade-ID exit matching, timestamp-based strategy matching,
and automatic replacement are removed. Their old messages are rejected as
unsupported input. Remove those TP/SL TradingView alerts. Generate only entry
messages shown on Profile → Messages; ProjectX Auto OCO manages filled positions.
A new entry cannot replace/cancel an existing managed execution; it is blocked
and is not replayed later. Persistent history and existing emergency latches are
preserved, not erased by this change.

A working limit entry expires 30 minutes after its original `signal.timestamp`
(the generated message uses `{{timenow}}`). This is independent of the configured
incoming-signal freshness limit, which still rejects stale alerts before entry.
The first successful reconciliation at/after that deadline requests cancellation
of the unfilled remainder and verifies the resulting order/account state. A
partially filled position keeps its protective brackets. A fully filled position
is not closed merely because 30 minutes elapsed. Pause, session entry cutoff and
exit reminders can apply earlier; risk/protection reports pause entries without automatically closing positions.
The deadline is derived from the persisted original timestamp across restart.

This is local cancellation, not a broker-side expiry parameter: the laptop and
API connection must remain available. A racing fill or network outage can prevent
cancellation at the exact deadline. Uncertain states block new entries and are
reconciled; no new entry is automatically replayed.

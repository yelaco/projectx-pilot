# Automatic trading: Start once, Pause when wanted

Use **Automations → your profile → Automatic execution**. The main view has one **Start automation / Pause automation** control, current status and recent execution outcomes. There is no daily date-entry or repeated session-start form. Routing, trading setup and emergency actions are collapsed below it.

## One-time configuration

Create or save the Trade Manager with just maximum planned risk per trade. Existing managers retain legacy limits until saved; saving pauses execution. Simplified policies have no account-floor, daily-loss or trade-count limits.

1. Configure ProjectX and discover your Practice account. Assign a single-account Trade Manager in the automation's Signal routing section, select automatic handling, pin the actual contract expiry, and choose Market or Limit. Existing defaults remain review-only.
2. Open **Trading setup**. Set estimated round-trip costs, slippage/deviation tolerance, and maximum contracts. Confirm real execution, Practice/Auto OCO, ordinary 17:00 Chicago session accounting and applicability of the reviewed calendar. Save with your administrator password. The selected account comes from the profile, and account eligibility is saved here too; no Practice execution visit is needed.
3. Configure the profile's shared/separate BUY/SELL field mappings, enable inbox receiving/tunnel, and copy the complete profile URL and messages into TradingView. The tunnel must only reach notification ingress, never the admin/order client. No setup action automatically starts execution.
4. Click **Start automation**. This explicit local action uses the existing authenticated login and CSRF protection; it does not require another password/date-entry form. New eligible alerts can now place actual ProjectX Practice orders. This is not application paper simulation.

Thereafter use **Pause automation** when you want to stop new entries. Pause preserves position monitoring and asks for cancellation of an entry remainder. Changing configuration, credentials or eligibility pauses the run. Only one profile/session can control the shared engine at a time; another profile cannot pause or flatten its execution.

The application does not require a Topstep support ticket/reference. Removing that local gate does not establish provider permission; confirm the intended workflow against the [official API policy](https://help.topstep.com/en/articles/11187768-topstepx-api-access).

## Running is separate from entry eligibility

Start records an in-memory running choice. The controller independently checks calendar, profile scope, account state, durable lockouts and current exposure. During verified open windows it obtains fresh execution permission through account/order/position reconciliation. At a normal session boundary it can do that again automatically, without another click.

- **Running · accepting eligible signals:** current session is authorized; each alert still passes fresh contract/account/history/quote/risk checks.
- **Running · market closed:** new entries are blocked; the controller waits for the next verified window.
- **Running · existing session lockout / legacy risk limit reached:** the durable daily lockout remains; the next verified ordinary session uses its own loss/trade accounting and must pass fresh reconciliation.
- **Running · monitoring existing execution:** an order/position or unresolved observation prevents new entries. The controller does not assume flatness.
- **Running · waiting for account readiness/calendar:** unknown data, unavailable trading permission or an unreviewed calendar cannot authorize an order.
- **Paused / needs review:** explicit Pause, configuration changes, uncertain placement, protective failure or an unsuccessful emergency operation stop the running choice. No silent retry or re-arming follows these critical faults.

Scheduled flattening still occurs. Positions are not held overnight because the runner remains enabled. A successful scheduled flatten can leave the runner waiting for the next session; failed/unconfirmed closing stops it for review. Closing requests use the existing persistent one-attempt latch and never blindly replay.

**Restart remains paused.** One-time setup is saved, but the running choice and execution permission are not restored. After recovery/reconciliation use the same single Start button. Never delete the execution database or bypass uncertainty to resume trading.

## Calendar scope

The bundled schedule revision is `topstep-2026-reviewed-2026-11-13`, reviewed against [Topstep trading hours](https://help.topstep.com/en/articles/8284206-when-and-what-products-can-i-trade) and [2026 holiday hours](https://help.topstep.com/en/articles/13350348-topstep-holiday-trading-hours). It supports **MNQ and MGC only**, with ordinary Chicago session accounting. Unknown instruments/years and merged-session accounting remain unsupported.

Normal entries end at 15:00 Chicago and the application flattens by 15:05. Published early-close deadlines get an additional five-minute flatten margin, with new entries ending five minutes before that. Weekends and published closed holidays are skipped. The timezone implementation handles DST independently of the desktop timezone.

These are bounded reviewed calendar data, not a live exchange-calendar API. Unexpected closures, platform restrictions and stale data still block/reject entries. Dates beyond the reviewed 2026 calendar wait for an update; the controller does not invent next year's schedule. The one-time confirmation must accurately cover the selected Practice account’s ordinary session model. A simplified Trade Manager needs no account floor and does not enforce MLL; legacy policies still require a verified fixed floor.

## Alert execution and recovery

The implemented path is authenticated webhook → SQLite inbox → local dispatcher → exact profile/account/contract binding → fresh risk/session/data checks → durable intent/tag → ProjectX market/limit order with mandatory Auto OCO → fill/protective monitoring. Automatic quantity is dynamically sized and capped by configured maximum contracts. Brackets use tick distances relative to actual fills; absolute prices can differ from the signal reference.

Each automatic alert is permanently claimed before any trading attempt. The engine separately reserves intent before transmission. Duplicates, conflicting IDs, stale alerts and claims interrupted by a crash never produce a speculative replay. Alerts received while paused/outside an authorized session, before current session permission or for another profile are blocked, not saved for later trading. Review-only profiles retain the original human inbox.

Existing exposure blocks another entry. No averaging down, reversal strategy, exit-only command or multi-account routing is inferred. Submitted means acknowledgement, not filled/protected. Watch the profile monitor and receipt outcomes. Manual import of a claimed automatic alert is refused.

The public receiver only persists notifications and has no ProjectX client/credentials. Its HTTP 202 `execution_status: queued` response confirms persistence, not an order or fill. The main process polls the local queue with bounded timers, calls ProjectX directly from the laptop, and reconciles active orders about every two seconds. Broker-side brackets remain essential during laptop/network outages; keep TopstepX available for independent emergency management.

## Verification and remaining limits

Offline tests exercise the real receiver, SQLite, dispatcher, manager/controller and execution engine against simulated gateway responses. They cover automatic BUY/SELL placement, brackets, duplicate/crash/timeout handling, continuous session rollover, weekend/holiday/DST handling, daily lockouts, explicit pause, scope changes and restart safety. No live order or activation was sent from development.

Still unsupported: multi-account execution, user-hub streaming, automatic MLL discovery, trailing floors, paper simulation, local strategy implementations, order modification/trailing entries and calendars outside the reviewed scope. See [risk management](risk-management.md), [order lifecycle](order-lifecycle.md) and [recovery](recovery-procedures.md).

## Starting after a manual Practice test

A paused automation and an armed manual Practice session are separate states of the shared execution engine. The profile status identifies an armed manual session instead of showing only Paused. Explicit Start can replace idle manual permission: it invalidates the manual preview, disarms manual entries, then performs the normal fresh account/order/position reconciliation before enabling automatic entries. Start itself submits no order. A durable active intent or any observed broker exposure blocks the switch. Another running automatic profile (including one waiting through closure) must be paused explicitly; the error names that profile. This transition never closes positions or bypasses lockouts, risk checks or protection.

## Signal-handling verification and automatic quantity

Regression coverage now renders the same shared/separate named-plot BUY/SELL templates used by the dashboard, substitutes simulated TradingView values, and passes the complete authenticated message through the actual receiver, SQLite inbox/claim, manager, risk sizing and execution engine to an offline gateway. It verifies pinned account/contract routing, calculated whole-contract quantity, signed stop/target offsets, duplicate suppression and blocking of wrong-token/wrong-symbol/stale/paused deliveries without per-alert local approval. Broker simulation now uses one-way OCO linkage for automatic fill/protection tests.

This is evidence for the local implementation, not proof that a live TradingView alert traversed Cloudflare and filled on the user's account. The remaining external verification must use the user's actual TradingView alert, without sending an extra synthetic delivery into an armed live profile.

Automatic quantity is `min(maximum_contracts, floor(risk_budget / estimated_risk_per_contract))`. The per-contract estimate is `stop_ticks * authoritative_tick_value + reserved_slippage_ticks * authoritative_tick_value + estimated_round_trip_fees_and_commissions`. Simplified managers supply their per-trade risk budget; legacy policies may further constrain it. Less than one contract is rejected. The receiver doesn't accept a caller-selected account or quantity. Practice API tickets instead retain the exact requested quantity and reject quantities outside risk/cap limits. Already-held exposure still blocks new entries.

Current generated signal IDs include profile, instrument, direction and TradingView bar time, allowing at most one distinct signal per direction/bar; a repeated ID with different content is a conflict, not another trade. Keep alert frequency consistent with this identity rule. Invalid/unresolved numeric placeholders are rejected rather than replaced with guessed values.

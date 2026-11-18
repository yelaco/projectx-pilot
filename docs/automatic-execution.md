# Automatic execution and account runners

Use **Automations → profile → Automatic execution**. Each profile has its own instrument scope, saved trading setup and Start/Pause control. Instruments are configured on the profile; Connection settings no longer require a second global allowlist. Profiles on distinct accounts run together; one profile can fan out to every account in its Trade Manager. No additional daemon, queue service or polling configuration is required.

## Configure once, then Start

1. Configure ProjectX credentials and refresh account discovery. Automatic targets must return `canTrade=true`, `simulated=true`, a finite balance and the pinned numeric account ID. Practice, Trading Combine and Express Funded are supported. Live Funded is excluded by [Topstep's API policy](https://help.topstep.com/en/articles/11187768-topstepx-api-access). No PRAC-name check applies to automatic profiles.
2. Create a Trade Manager with planned risk per trade and one or up to 20 explicit target accounts. Each account gets that risk budget independently. Multi-account managers require per-trade-only risk; they have no MLL/floor, daily-loss/trade-count or aggregate portfolio protection. Legacy single-account managers retain their saved limits until explicitly changed.
3. In the profile's routing section select the manager, automatic handling, actual contract expiry and Market or Limit. In **Trading setup**, set fees/slippage, maximum contracts and optional deviation guard. Confirm all target accounts, Auto OCO and reviewed session coverage, then save with your administrator password. This grants/revokes the whole displayed target set; it does not start execution.
4. Configure shared or separate BUY/SELL mappings, enable notification receiving, and copy this profile's full webhook URL and generated messages into TradingView. Numeric stop/target placeholders must actually resolve. Existing tokens, URLs and messages are unchanged by the runner upgrade.
5. Click **Start automation**. Each target reserves its account and performs fresh reconciliation/arming independently. Inspect **Execution runners** for every target's readiness. A temporarily unavailable account can wait while other targets are ready. New alerts execute on eligible targets without individual approval.

Start is explicit local authorization, not a support-ticket gate. The application requires no ticket/reference; the operator must confirm provider permission for the intended remote-trigger workflow. Setup, enabling the receiver and installing/upgrading never arm orders.

## Ownership and controls

One running profile owns each account. Different contracts/strategies on the same account do not bypass this rule. Start reports the conflicting account/profile and does not partially reserve a manager's targets. To change owner, pause the old profile and let its working/unresolved execution finish or explicitly flatten and verify it first. Manual Practice tests can run on a separate account. Starting automatic execution can replace an idle manual permission on its own target, but never an unresolved manual order.

**Pause profile/account** stops new entries and retains position management. Only wholly unfilled parents with no current or previously verified exposure may be cancelled by the engine; partial remainders require operator review. **Pause all entries** is a persistent global gate covering automatic and manual arming. **Resume global entry gate** clears that gate but leaves profiles paused until Start. Password-confirmed **Flatten account/profile/all** requests cancellation and closing; inspect each runner for verified flatness and any failure. Flatten all includes active manual recovery. Pause cancels preflight before reservation; an attempt already durably reserved for submission can complete its bounded one-time request. Pause cannot recall it.

Profile/account/risk changes pause affected profiles and require setup confirmation for changed scope. Global connection or receiving changes pause the applicable runs. Existing trade management uses the policy captured with that trade. Changes never revoke existing protective orders or replay old deliveries.

## Continuous sessions

Start stays requested while this process runs, waiting through known closures and normal session limits. Before a new verified session, each account must pass fresh eligibility, flatness, risk and lock checks. Fault locks, uncertainty and protective failure require review; they do not silently reset at a new Start.

**Restart remains paused.** Saved setup is configuration, not execution authorization. Recover/reconcile, then Start the desired profiles again. A persistent global pause remains active after restart. Never delete a ledger to bypass a lock.

Trading hours belong to the Trade Manager, not an instrument-name list. Each manager has one recurring weekday window in America/Chicago: start, last entry, and flatten. Defaults preserve the previous 17:00 → 15:00 last entry → 15:05 flatten. A start at/after 17:00 means the previous evening; earlier starts mean the trading date itself. Last entry must be at least two minutes before flatten, and flatten must be no later than 15:08 Chicago. DST is applied using local clock times.

Use a narrower window for products with earlier closes or pauses. For example, a CBOT agricultural daytime manager can use 08:30 → 13:10 last entry → 13:15 flatten; an overnight manager can use 19:00 → 07:35 → 07:40. These are conservative configuration examples based on [Topstep's published product hours](https://help.topstep.com/en/articles/8284206-when-and-what-products-can-i-trade), not automatic product-hours discovery. One manager has one window; do not configure a window spanning a product's daily pause. Confirm applicability to every selected contract when saving the profile's trading setup. ProjectX contract metadata provides prices/ticks, not verified trading hours.

Reviewed [2026 holiday cutoffs](https://help.topstep.com/en/articles/13350348-topstep-holiday-trading-hours), revision `topstep-2026-reviewed-2026-11-13`, can only shorten the manager's window. The application sets an exit reminder five minutes before the published position-close cutoff and ends new entries at least five minutes before that holiday bound. A holiday with no overlap is skipped; weekends and known closed days are skipped. Unknown calendar years remain blocked pending a reviewed update. Account session accounting remains 17:00 Chicago. General/per-manager operation dates and the configured Forex Factory filter also apply; missing news feed coverage alone does not block a day.

Existing managers with no saved schedule inherit the unchanged default without altering their setup scope. Editing a manager's schedule pauses its profiles and invalidates their saved setup scope; confirm the new schedule in each affected profile's Trading setup before Start. Other managers are unaffected. Existing executions retain their captured policy and closing window; schedule edits never widen or replace their protection/management deadlines. Manual Practice authorizations are intersected with the manager's schedule too, without widening the locally verified session. Active monitoring schedules a wake at future entry/flatten/expiry deadlines rather than waiting beyond them for the adaptive backstop.


## Delivery, sizing and protection

The receiver commits the alert before notifying the bot through a private Unix datagram socket. SQLite is authority; a lost notification is covered by a 30-second scan. The dispatcher atomically freezes/claims the complete target set before independent account workers run. Each target calculates its own quantity, reserves a durable intent and submits at most once. There is no cross-account atomic broker transaction: partial execution is possible and shown per account. Successful targets are not rolled back because another account failed.

Automatic quantity is `min(maximum_contracts, floor(risk_budget / estimated_risk_per_contract))`. Per-contract risk includes stop ticks × authoritative tick value, slippage reserve and round-trip fees. Less than one contract is rejected. Alert payloads cannot choose account or quantity. Manual Practice tickets keep the exact requested quantity instead. Existing exposure/working orders block another entry; averaging down and martingale are unsupported.

Every submission includes signed Auto OCO bracket offsets based on positive UI/risk distances. Brackets attach relative to actual fills; absolute stop/target prices can differ from the signal's reference after entry slippage. Acknowledgement is not a fill. Partial fills and protective sibling linkage/quantity/prices are reconciled. Missing/inconsistent protection reports attention and locks entries only on the affected account; it never automatically closes positions or changes SL/TP. A wholly unfilled limit entry can expire 30 minutes after the original signal timestamp, or earlier for Pause/session restrictions. Once exposure has been observed, the remaining entry and brackets are left to the operator/broker. TP/SL exit alerts and replacement-entry strategies are not supported.

Alerts arriving before Start/arming, while busy/disarmed or outside the exact authorized scope receive permanent blocked outcomes. They do not fire when readiness changes. Duplicate IDs never submit again. New routing does not expand an old claim. A crash leaves claimed targets interrupted or reconciliation-owned, never replayable. Review-only profiles remain a human inbox and do not submit.

Generated IDs include profile, instrument, direction and TradingView bar time: one distinct signal per direction/bar. Conflicting content under the same ID is rejected. Unresolved numeric placeholders are rejected rather than guessed.

## Monitoring and latency

**Execution runners** shows ownership, per-account readiness, working lifecycle, global gate, user stream and per-target delivery results. It refreshes every three seconds without changing forms. Inbox shows aggregate and individual outcomes.

Recorded intervals are HTTP receiver arrival → worker, checks → submission, submission → acknowledgement, and total arrival → ProjectX acknowledgement. The latter includes local request-governor waiting and network time; it is not fill latency. The measurement begins when the local receiver handles the request, excluding TradingView/Internet/tunnel delivery before that point.

JWT/HTTP connections, metadata cache and per-contract quote feeds are shared; quotes warm at arming. Fresh contract/account/positions/orders preflight runs concurrently. User events wake account reconciliation and reconnect restores subscriptions/full checks. REST reconciliation remains authoritative. Active automatic backstop is `max(5s, 3s × active accounts)`; events coalesce at one second per account. Broker-side OCO is required during observation gaps/offline periods.

The official API limit is 200 requests/minute. The bot caps itself at 180 total, 140 reads and eight concurrent requests, leaving capacity for mutations. Fanout is concurrent within this shared budget, not unlimited or guaranteed simultaneous. A saturated budget can delay/deny entries within bounded deadlines. Fresh risk/exposure checks are never skipped for speed.

## Upgrade and verification

When all managed accounts are verified flat and no intent remains unresolved, run:

```sh
./scripts/install-user-service.sh
```

This builds/replaces both bot and receiver, including socket wake support. Existing named tunnel service/hostname requires no restart or change for this upgrade. Existing valid single-profile setup is migrated as configuration where its exact saved scope/calendar still matches; otherwise the profile clearly requests setup confirmation. Multi-account setups must be saved explicitly. The old global SQLite reservation index is migrated automatically; per-account uniqueness remains. No automatic Start is restored.

Offline tests cover real HTTP receiver → SQLite → Unix wake → dispatcher → independent execution engines → mocked gateway acceptance, distinct profiles, whole-target claims, overlaps, partial outcomes, ambiguity, duplicate/restart suppression, account locks, shared subscriptions/cache, stream reconnect and rate budget. No live ProjectX writes or TradingView/Cloudflare delivery were performed in development. Mock acceptance timings do not predict real provider latency. Paper simulation, automatic MLL discovery, local strategies and arbitrary order modification remain outside this implementation.

## Operator-only exits

The legacy schedule field named `flatten` is an exit reminder, not an automatic exit. Risk thresholds, protection discrepancies and technical errors only pause/report. Last verified exposure persists across restart as historical evidence and cannot authorize trading or prove flatness. Only password-confirmed local emergency Flatten controls may close exposure or cancel protection. Broker SL/TP executes independently. Legacy daily-loss/floor policies no longer enforce exits or guarantee account-floor protection. Optional Discord reports are configured in Connection settings; see [attention reports](attention-reports.md).

## Recovery and signal allocation

A Start made in the current process can wait through technical pauses and resume after resolved execution history, two fresh flat/no-order observations, fresh market/account checks and ordinary session/risk arming. Manual Pause, emergency Flatten, configuration revocation and restart cancel that running choice. Non-overridable risk locks and ambiguous/legacy repair cases are not waived. See [automatic recovery](order-lifecycle.md#automatic-recovery-of-a-running-profile).

Multi-account Trade Managers now offer Copy (every configured target) or Distribution (one ready target per signal, round-robin). Existing configurations default to Copy. No-ready alerts remain durably blocked; failures after selection never trigger a fallback. See [signal distribution](signal-distribution.md).

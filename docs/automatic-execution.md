# Automatic execution and account runners

Use **Automations → profile → Automatic execution**. Each profile has its own saved trading setup and Start/Pause control. Profiles on distinct accounts run together; one profile can fan out to every account in its Trade Manager. No additional daemon, queue service or polling configuration is required.

## Configure once, then Start

1. Configure ProjectX credentials and refresh account discovery. Automatic targets must return `canTrade=true`, `simulated=true`, a finite balance and the pinned numeric account ID. Practice, Trading Combine and Express Funded are supported. Live Funded is excluded by [Topstep's API policy](https://help.topstep.com/en/articles/11187768-topstepx-api-access). No PRAC-name check applies to automatic profiles.
2. Create a Trade Manager with planned risk per trade and one or up to 20 explicit target accounts. Each account gets that risk budget independently. Multi-account managers require per-trade-only risk; they have no MLL/floor, daily-loss/trade-count or aggregate portfolio protection. Legacy single-account managers retain their saved limits until explicitly changed.
3. In the profile's routing section select the manager, automatic handling, actual contract expiry and Market or Limit. In **Trading setup**, set fees/slippage, maximum contracts and optional deviation guard. Confirm all target accounts, Auto OCO and reviewed session coverage, then save with your administrator password. This grants/revokes the whole displayed target set; it does not start execution.
4. Configure shared or separate BUY/SELL mappings, enable notification receiving, and copy this profile's full webhook URL and generated messages into TradingView. Numeric stop/target placeholders must actually resolve. Existing tokens, URLs and messages are unchanged by the runner upgrade.
5. Click **Start automation**. Each target reserves its account and performs fresh reconciliation/arming independently. Inspect **Execution runners** for every target's readiness. A temporarily unavailable account can wait while other targets are ready. New alerts execute on eligible targets without individual approval.

Start is explicit local authorization, not a support-ticket gate. The application requires no ticket/reference; the operator must confirm provider permission for the intended remote-trigger workflow. Setup, enabling the receiver and installing/upgrading never arm orders.

## Ownership and controls

One running profile owns each account. Different contracts/strategies on the same account do not bypass this rule. Start reports the conflicting account/profile and does not partially reserve a manager's targets. To change owner, pause the old profile and let its working/unresolved execution finish or explicitly flatten and verify it first. Manual Practice tests can run on a separate account. Starting automatic execution can replace an idle manual permission on its own target, but never an unresolved manual order.

**Pause profile/account** stops new entries and retains position management. A working entry remainder is cancelled by the engine. **Pause all entries** is a persistent global gate covering automatic and manual arming. **Resume global entry gate** clears that gate but leaves profiles paused until Start. Password-confirmed **Flatten account/profile/all** requests cancellation and closing; inspect each runner for verified flatness and any failure. Flatten all includes active manual recovery. Pause cancels preflight before reservation; an attempt already durably reserved for submission can complete its bounded one-time request. Pause cannot recall it.

Profile/account/risk changes pause affected profiles and require setup confirmation for changed scope. Global connection or receiving changes pause the applicable runs. Existing trade management uses the policy captured with that trade. Changes never revoke existing protective orders or replay old deliveries.

## Continuous sessions

Start stays requested while this process runs, waiting through known closures and normal session limits. Before a new verified session, each account must pass fresh eligibility, flatness, risk and lock checks. Fault locks, uncertainty and protective failure require review; they do not silently reset at a new Start.

**Restart remains paused.** Saved setup is configuration, not execution authorization. Recover/reconcile, then Start the desired profiles again. A persistent global pause remains active after restart. Never delete a ledger to bypass a lock.

The reviewed calendar revision is `topstep-2026-reviewed-2026-11-13`, based on official [trading hours](https://help.topstep.com/en/articles/8284206-when-and-what-products-can-i-trade) and [2026 holidays](https://help.topstep.com/en/articles/13350348-topstep-holiday-trading-hours). Continuous automation supports MNQ/MGC only in that reviewed year. Normal entries end at 15:00 Chicago, flatten by 15:05; early closes get an additional margin. DST uses America/Chicago independently of desktop timezone. Unknown periods/instruments and merged holiday sessions do not authorize trading. General and per-manager operation dates and the configured Forex Factory US FOMC filter are applied per account. Missing feed coverage alone does not block a day.

## Delivery, sizing and protection

The receiver commits the alert before notifying the bot through a private Unix datagram socket. SQLite is authority; a lost notification is covered by a 30-second scan. The dispatcher atomically freezes/claims the complete target set before independent account workers run. Each target calculates its own quantity, reserves a durable intent and submits at most once. There is no cross-account atomic broker transaction: partial execution is possible and shown per account. Successful targets are not rolled back because another account failed.

Automatic quantity is `min(maximum_contracts, floor(risk_budget / estimated_risk_per_contract))`. Per-contract risk includes stop ticks × authoritative tick value, slippage reserve and round-trip fees. Less than one contract is rejected. Alert payloads cannot choose account or quantity. Manual Practice tickets keep the exact requested quantity instead. Existing exposure/working orders block another entry; averaging down and martingale are unsupported.

Every submission includes signed Auto OCO bracket offsets based on positive UI/risk distances. Brackets attach relative to actual fills; absolute stop/target prices can differ from the signal's reference after entry slippage. Acknowledgement is not a fill. Partial fills and protective sibling linkage/quantity/prices are reconciled. Missing/inconsistent protection invokes the existing fail-safe and locks only the affected account. Unfilled limit remainder expires 30 minutes after the original signal timestamp, or earlier for Pause/session management; brackets on filled exposure remain managed. TP/SL exit alerts and replacement-entry strategies are not supported.

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

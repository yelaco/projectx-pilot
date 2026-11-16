# Recovery procedures

Keep the official TopstepX platform available independently of the laptop application. Server-side Auto OCO brackets are required for every bot order. A laptop cannot cancel or flatten while offline, suspended or powered off. Stopping the service does not promise a flat account. Flatten and verify first before stopping, upgrading or suspending.

Startup is disarmed. A process-level lock prevents two services from managing the same state directory. Persisted intents in `execution.db` are loaded and reconciled against account order history (durable customTag/order ID), current working orders and positions. Recovery never infers flatness from restart and never replays order placement. The installer refuses an upgrade while an active/unresolved ledger entry exists. Do not run this account through several state directories or other bots.

An ambiguous placement is displayed as uncertain. Do not click another order, regenerate the same signal, delete the database or weaken checks. The loop can recognize an order that appears later. If it stays absent/unresolved, inspect the platform and contact ProjectX/Topstep support; there is no automatic uncertainty override. Proven terminal no-fill outcomes require repeated flat observations. Filled terminal outcomes also require closing-fill history and no remaining working orders. Session lockouts survive restart and cannot be cleared by re-arming.

A protective failure immediately disarms and attempts the documented emergency sequence once: cancel/verify entry parents, close observed positions, cancel orphans after observing flatness. The latch is saved before mutations. If any request times out, a cancellation remains pending or flatness is unconfirmed, manage the account in TopstepX immediately. The service reports failure and does not blindly replay close requests. A later flat observation can complete the intent while the session lockout remains.

Connection failure pauses new entries; account/order/position REST reconciliation runs after connectivity returns. The market quote stream reconnects and resubscribes with bounded exponential backoff and JWT renewal. With fresh REST-verified linked SL/TP, unavailable quotes do not flatten exposure or create a session lock. Per-trade-only policies do not require quotes for ongoing management. Legacy daily-loss/floor policies pause and disarm new entries, stop continuous running, and report that quote-based risk checks are unavailable; read-only protection reconciliation and exit reminders continue. Fresh quotes resume risk calculations but never automatically restart entries. Missing or inconsistent protection invokes an attention-only fail-safe: entries pause, exposure and protective orders are untouched. User-hub events wake account REST reconciliation; reconnect restores subscriptions and requests fresh checks. Automatic active backstop is max(5 seconds, 3 seconds × active accounts), with per-account event wakes coalesced to one second; manual tests retain a two-second active backstop. There can be observation gaps and API-budget delays.

Account discovery failures keep the previous snapshot but mark it stale. That dashboard snapshot is separate from execution readiness. `/healthz` currently reports discovery health only; inspect the execution monitor and Activity log too. Do not treat an HTTP health response or `canTrade:true` as proof of protective coverage.

SQLite files contain signal IDs, intent references, observations, session lockouts and audit activity. Keep the whole private state directory; never delete individual files to clear a gate. Back up only after shutdown with the account independently verified flat. Configuration and credential updates disarm, and connection/credential changes are refused while an intent is active. Password changes invalidate the applicable administrator authorization.

For service diagnosis:

```sh
systemctl --user status projectx-bot.service
journalctl --user -u projectx-bot.service -n 100
journalctl --user -u projectx-bot.service -f
```

Never paste API keys, administrator passwords, webhook tokens or raw payloads into support logs. Local logs use sanitized lifecycle messages rather than raw gateway response bodies.

Automatic-mode recovery also consumes the complete account target set in `target_jobs` before dispatch (legacy `automation_receipts` remain historical). A claimed alert is never replayed. On restart a processing claim is resolved against the durable intent (interrupted if none, uncertain/reconciliation-owned if present). New automatic arming accepts only arrivals after that arming; old/unarmed alerts receive blocked outcomes rather than firing later. Stop/pause/revocation preserve exposure and protection; only wholly unfilled entries without current or previously verified exposure may be cancelled. Saved setup never restores execution permission. See [automatic execution](automatic-execution.md).

Continuous-mode running intent is in-memory and is not restored at startup. Saved one-time setup does not authorize startup trading. During normal uninterrupted operation a requested run can wait through closures and daily risk limits and reconcile into the next reviewed window without another click. Uncertain writes, protective failures and unsuccessful emergency actions clear the run choice; their durable intent/lockout/latches still govern recovery. An active runner is not permission to hold positions overnight.

Submission diagnostics now retain a separate `SubmissionFailure` field in the durable intent and execution monitor. It contains only fixed text and numeric HTTP/API codes; raw provider messages, request bodies and credentials are excluded. Reconciliation may replace the lifecycle message, but cannot erase this original diagnostic. This is diagnostic information, not proof of rejection or permission to retry. The historical generic “submission requires reconciliation” text was used for both ambiguous transport results and API failures; it never established that an order filled or failed. Old intents cannot recover an error code that was never stored.

For an unresolved live attempt, keep the monitoring service running, inspect the exact account's positions, working orders and order history in TopstepX, verify protection on any exposure, and use official platform management/support if necessary. Do not run the upgrade installer, restore/delete the ledger, generate another signal ID, or resubmit to try to clear an unresolved placement. The installer intentionally refuses active/unresolved state.

## Runtime-only provider rejection reason

For a new explicit placement failure, the authenticated execution monitor additionally shows ProjectX's `errorMessage` as bounded, credential-redacted, escaped text. It is linked to the failed intent ID and kept only in process memory until the next placement or restart. It is excluded from the error string, logs, audit trail and SQLite payload; only safe numeric diagnostics persist. Do not restart before reading this detail. Older attempts cannot recover an errorMessage that was discarded. Code 2 alone identifies a rejection category, not a root cause; do not assume every rejection means incorrect bracket mode.

## Reviewing the known one-way OCO false alarm

A **bot local safety lock** is separate from ProjectX/TopstepX `canTrade`. Restarting does not clear it. The old reciprocal-only protection checker could close an otherwise correctly bracketed order and leave this local fault lock.

After updating the flat/reconciled installation, open **Practice execution → Review local lock from the old OCO checker**. Select the closed execution, enter the current administrator password, confirm the review, and click **Verify flatness & resolve local OCO lock**. This explicitly approved local action:

- Supports only the legacy exact OCO-checker reason, this session, the saved Practice account, a closed one-contract intent and its retained emergency latch.
- Requires no active/unresolved execution on the scoped engine/account and no running/armed session.
- Retrieves matching parent/tag/fill history and exactly two cancelled, unfilled children. Their one-way sibling relationship, side, quantity and tick-derived prices must pass the corrected checker.
- Authenticates/discovers the current PRAC simulated account and obtains two fresh observations of zero positions and zero working orders, separated by three seconds.
- Atomically records the repair in `execution_recoveries` and deletes only the matching local lock, conditional on the execution remaining closed and no active reservation on that account. Historical intents, duplicate claims and emergency latches are unchanged.
- Makes no placement, modification, cancellation or closing requests. It leaves execution paused; arming/Start remains a separate explicit action with fresh normal checks.

Daily/MLL locks, ambiguous submissions, unverified protection, changed locks, nonmatching accounts/history, read errors, cancellation and exposure changes are denied. This is not a generic force-unlock button, automatic fault recovery, or permission to retry the old signal. It was verified against mocked gateway responses; no installed lock was cleared during development. After a successful review use a new preview/test or a new eligible alert, never replay the old signal ID.

## Multiple account recovery

Automatic intents recover into separate account engines, with ownership retained for nonterminal executions. Manual recovery excludes automatic intents. Constructor/configuration loading never consumes a claimed target before process ownership: interrupted-claim recovery runs only when the lease-owning fleet starts. Every unfinished target is processed, including records beyond the dashboard display limit. No recovered job is resubmitted. An intent-backed claim becomes reconciliation-owned/uncertain; a claim without an intent becomes interrupted. Other accounts continue monitoring independently.

Global Pause is stored in SQLite and survives restart. Start intent never survives restart, even if the global gate is open. After each account is independently checked, use Start on the desired profiles. Do not use a new profile ID/account assignment to replay an old delivery. The complete target set is immutable after claim. Flatten-all reports per-account progress/errors through Activity and each runner; an accepted close request is never represented as verified flatness. The account-scoped legacy OCO review is also available on Execution runners and cannot clear unrelated account locks.

Stale-quote locks from older versions can be reviewed locally on the account runners page (or manual Practice execution page). The password-confirmed action requires this session’s closed fault intent, the exact original stale-quote reason, matching terminal filled entry history, saved verified protection evidence, and two fresh flat/no-working-order observations at least three seconds apart. An atomic account/session/intent audit clears only that reason. Daily-loss locks, unresolved intents, emergency latches and delivery claims remain unchanged; execution stays paused and requires explicit local Start/arming.

## Operator-only closing policy

Automatic monitoring never closes positions or cancels protective orders. Previously verified exposure is retained durably as historical evidence, not live authority. Errors, discrepancies, risk thresholds and scheduled reminders require operator attention and pause new entries. Working orders on apparently flat accounts are not automatically cleaned up. Only a fresh authenticated local emergency Flatten action may send closing/cancellation requests; legacy emergency latches are retained and never replayed. Broker-side SL/TP and provider actions remain independent. See [attention reports](attention-reports.md) for optional Discord delivery.

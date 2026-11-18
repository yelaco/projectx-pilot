# Order lifecycle

`internal/execution` now submits locally approved market and limit orders to one verified simulated account (Practice, Trading Combine or Express Funded for automatic profiles; PRAC only for manual tests). Defaults remain read-only and disarmed. See [manual execution](manual-execution.md) for setup and operational boundaries.

Preview performs fresh contract/account/exposure/history/quote/session/risk checks without submitting. Submit revalidates the preview and reserves a unique signal ID and per-account active-intent slot in SQLite before the network call. A unique `pxbot-…` customTag is persisted. There is no automatic placement retry.

States: `submitting → accepted → partial/protected → closed`. Terminal alternatives are `rejected` and `cancelled`; unresolved responses enter `uncertain`. Missing or inconsistent protection enters `attention-required`, disarms, persists a session lockout and reports for operator review. No automatic closing or protective-order cancellation occurs. Legacy daily-loss breaches use `risk-lockout` with the same non-mutating behavior. State/message changes are included in the activity log. Database failure prevents further entries.

Gateway acknowledgement is not a fill. Reconciliation reads order history, working orders and positions. It binds the parent to both durable tag and ID, checks order type/side/size/contract, verifies observed partial exposure and validates opposite-side stop/limit target coverage, remaining quantities, parent links and one-way or reciprocal OCO links. Multiple fill pairs are checked against pair tick distances and quantity-weighted actual average entry. Unsupported/inconsistent observations fail closed.

Limit expiry (30 minutes from the original signal timestamp), pause or an entry-window cutoff cancels only a wholly unfilled parent with no current or previously verified exposure. Partial-fill remainders are left for operator review. Flat terminal observations must repeat after at least three seconds; filled-and-closed orders additionally require closing-fill evidence and no working orders. Working orders on a flat snapshot are reported, never automatically cleaned up. Separate API snapshots never authorize cancellation of protective orders.

A verified exposure snapshot and UTC verification time are saved on the durable intent and retained through data failures and restart. This is historical evidence, not proof of current exposure or a substitute for fresh reconciliation. Restart never restores arming or replays placement or closing. Network, quote and accounting failures pause/report while existing broker orders remain untouched.

Only the authenticated local operator's explicit emergency Flatten action can invoke position closing. It cancels and verifies entry parents, closes the selected account's positions while retaining protective children, then cancels orphans after observing flatness. The one-attempt emergency latch is durable before any emergency mutation and prevents replay after ambiguous results or restart. Session locks remain. Automatic monitoring, including scheduled reminders, cannot invoke this path.

The captured schedule's legacy `Flatten` timestamp is now an exit reminder and entry restriction, not an automatic exit. Reaching it with exposure reports attention and stops continuous running; the operator is responsible for closing as required. Legacy daily-loss and fixed-floor checks are observations/entry gates, not enforced exit or floor protection. The provider's own restrictions and broker SL/TP remain independent.

No live gateway write or Practice fill was transmitted to test this change. Current account fanout/user events are described in [automatic execution](automatic-execution.md).

## One-way OCO relationship correction

A read-only query of the operator's completed Practice test on 2026-11-13 showed a filled entry, stop and target sharing that entry's parent ID, with the target linked to the stop and no outgoing stop-to-target link. The original checker incorrectly required reciprocal links and closed that trade. Verification now accepts either direction of a single explicit sibling link or reciprocal links; it never infers OCO from prices alone. Any conflicting link, wrong parent/account scope/contract/side, non-open leg, duplicate observation, coverage mismatch or incorrect price distance still fails closed. Each leg must resolve to exactly one valid opposite-type sibling with matching remaining quantity.

The first management trigger (`HaltReason`) and typed order observations (`HaltOrders`) are now captured durably before emergency handling. Progress messages and eventual flatness no longer hide the cause. The old closed record cannot reconstruct observations that were overwritten, so this change does not retrofit evidence or clear existing session lockouts. Restart/upgrade never restores arming or replays this order. Offline regression tests model the observed MGC layout, valid one-way/reciprocal links and invalid protection cases; the implementation fix was not validated by placing a live order.

## Account-scoped fanout

Each target has its own lifecycle, frozen risk policy and deterministic local intent ID linked to the original delivery ID. SQLite allows active intents on different accounts and forbids two on one account. All account targets are claimed atomically before bounded independent dispatch. Accepted, blocked, uncertain and interrupted target outcomes are separate from later fill/protection state. Failure on one account does not cancel another account's successful trade. Socket notifications never carry trading authority. Restart recovers every account's intent and never replays target placement. Timing records separate arrival/dispatch, preflight, submission and gateway acknowledgement. See [architecture](architecture.md).

## Entry-fill observation lag

Order history, working orders and positions are separate broker reads, not an atomic snapshot. A position can therefore be visible before the parent order's reported fill quantity catches up.

When the selected contract, direction and position quantity fit the durable intent and current linked SL/TP passes verification, a position larger than the reported entry fill enters `reconciling`. The active intent continues blocking all new entries. Pilot persists the pending timestamp and last reported parent fill quantity, and schedules another normal reconciliation within two seconds without issuing immediate retry requests or mutating orders. Protection remains unknown in health telemetry until a complete reconciliation succeeds.

If a fresh observation at least two seconds later still disagrees, Pilot records the existing exposure-discrepancy attention lock and pauses entries. Repeated event wakes cannot shorten this minimum observation window. A backwards clock also fails closed. If the history catches up, normal verification resumes without creating a fault lock or clearing the operator's running choice. Restart preserves pending evidence but never restores arming or the running choice.

This window does not defer wrong-contract, wrong-direction, excess/invalid quantity, missing/inconsistent protection, or existing attention/risk locks. A confirmed lock still requires its existing review procedure; later recovery never silently clears it, and original triggers remain retained. No automatic closing, protective cancellation, order retry, broker probe or legacy lock repair was added. Historical locks are not retroactively cleared by this change.

## Automatic recovery of a running profile

An explicit Start is now retained in process memory separately from temporary engine arming. A technical pause shows `RecoveryPending` / “Waiting for recovery”; it does not discard that Start. Operator Pause, emergency Flatten, relevant configuration edits, shutdown and restart revoke it. Nothing restores a Start from disk.

While an execution is active, uncertain, submitting or needs emergency reconciliation, the account stays occupied and no new entry is armed. After terminal reconciliation, the controller requires two fresh flat/no-working-order observations at least three seconds apart, eligible account identity and fresh market data. It then performs the ordinary full arming/risk/session checks again. Availability failures reset the observation pair. Recovery observations use the existing bounded runner retry cadence; they are not extra health-check probes.

Known technical exposure/protection/accounting locks may be released automatically only for a matching closed execution created under that same in-process Start, account, profile, contract and session. SQLite compares the exact unchanged execution payload and lock reason, rejects any active account intent or risk/emergency evidence, and atomically records an `execution_lock_recoveries` audit entry. It does not alter the original trigger, history, acknowledgements, ownership, intent/placement claims or mutation latches. This is evidence of a resolved fault, not acknowledgement of a continuing one.

Daily-loss/trade-count/liquidation locks, ambiguous-write/unknown reasons and legacy stale-quote/OCO repairs are not automatically waived. A later verified session may pass its own normal checks without removing the previous session's risk lock. Specialized legacy repairs still revoke running permission and require another explicit Start. Historical locks predating this Start still require review. Protection verification for an open position never authorizes adding another entry.

Rearming has a new arming timestamp: alerts received before recovery or while disarmed remain blocked and are never replayed or reassigned. Recovery does not place, close, cancel or modify broker orders. Existing positions and their SL/TP remain operator-controlled.

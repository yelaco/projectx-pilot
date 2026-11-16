# Order lifecycle

`internal/execution` now submits locally approved market and limit orders to one verified simulated account (Practice, Trading Combine or Express Funded for automatic profiles; PRAC only for manual tests). Defaults remain read-only and disarmed. See [manual execution](manual-execution.md) for setup and operational boundaries.

Preview performs fresh contract/account/exposure/history/quote/session/risk checks without submitting. Submit revalidates the preview and reserves a unique signal ID and global active-account slot in SQLite before the network call. A unique `pxbot-…` customTag is persisted. There is no automatic placement retry.

States: `submitting → accepted → partial/protected → closed`. Terminal alternatives are `rejected` and `cancelled`; unresolved responses enter `uncertain`. Missing or inconsistent protection enters `protective-failure`, disarms, persists a session lockout and invokes the one-attempt flatten policy. State/message changes are included in the activity log. Database failure prevents further entries.

Gateway acknowledgement is not a fill. Reconciliation reads order history, working orders and positions. It binds the parent to both durable tag and ID, checks order type/side/size/contract, verifies observed partial exposure and validates opposite-side stop/limit target coverage, remaining quantities, parent links and one-way or one-way or reciprocal OCO links. Multiple fill pairs are checked against pair tick distances and quantity-weighted actual average entry. Unsupported/inconsistent observations fail closed.

Limit expiry (30 minutes from the original signal timestamp), pause or an earlier entry-window cutoff cancels the unfilled entry remainder, preserving protection for existing fills. Flat terminal observations must repeat after at least three seconds; filled-and-closed orders additionally require closing-fill evidence. Orphan children are cancelled after flatness and absence is rechecked. Restart never restores arming or replays an intent; it resumes reconciliation. Network failures pause new entries and require current REST observations after reconnection.

The configured emergency policy cancels and verifies entry parents, closes positions while retaining existing protective children, then cancels orphans after observing flatness. A durable emergency latch prevents replay after ambiguous results or restart. If cancellation, closing or flatness cannot be confirmed, use the official platform immediately. Session lockouts survive restart and are keyed to the verified ordinary Chicago trading session. Merged holiday sessions are unsupported.

Remaining limitations: two-second REST observation gap, no user-hub stream, no order modification, no general manual-position manager, no automatic ambiguity override and no multi-account routing. No live gateway write or Practice fill was tested during development.

Scheduled closures use a `flattening` state and daily-loss closures use `risk-lockout`. Successful scheduled closing can preserve a user-requested continuous run for a later verified session; uncertain/failed closing and protective faults stop the running choice. This does not change intent idempotency or the one-attempt emergency latch.

## One-way OCO relationship correction

A read-only query of the operator's completed Practice test on 2026-11-13 showed a filled entry, stop and target sharing that entry's parent ID, with the target linked to the stop and no outgoing stop-to-target link. The original checker incorrectly required reciprocal links and closed that trade. Verification now accepts either direction of a single explicit sibling link or reciprocal links; it never infers OCO from prices alone. Any conflicting link, wrong parent/account scope/contract/side, non-open leg, duplicate observation, coverage mismatch or incorrect price distance still fails closed. Each leg must resolve to exactly one valid opposite-type sibling with matching remaining quantity.

The first management trigger (`HaltReason`) and typed order observations (`HaltOrders`) are now captured durably before emergency handling. Progress messages and eventual flatness no longer hide the cause. The old closed record cannot reconstruct observations that were overwritten, so this change does not retrofit evidence or clear existing session lockouts. Restart/upgrade never restores arming or replays this order. Offline regression tests model the observed MGC layout, valid one-way/reciprocal links and invalid protection cases; the implementation fix was not validated by placing a live order.

# Copy and round-robin signal distribution

Implemented behavior: multi-account Trade Managers have a **Multiple-account behavior** setting. It controls signal allocation, not permission to trade. Saving changes pauses affected profiles and preserves active executions' captured configuration. Review any changed setup scope and explicitly Start the affected profiles again.

| Mode | One incoming signal |
| --- | --- |
| Single-account routing | One configured account |
| Multiple accounts / Copy | A durable target job for every configured account; independent outcomes |
| Multiple accounts / Distribution | One durable selected target, rotating among known ready accounts |

Existing policies without `multi_account_mode` keep Copy behavior. Values are `copy` and `round_robin`; round-robin requires 2..20 configured accounts. All configured targets remain explicitly authorized and reserved by the running profile; distribution does not silently change the manager's account scope. Broker/risk/session/protection checks apply independently to the account actually selected. Quantities are calculated by the existing risk engine, not split between accounts.

## Selection and durable rotation

The configured account order defines the rotation. Selection starts after the last selected account, skips accounts known to be paused, unarmed, busy, occupied or outside the exact started profile/scope/arming time, and chooses the next ready account. Readiness uses existing runner snapshots without extra broker probes. It is provisional: the selected account's fresh authoritative preflight can still reject the signal. Selection never substitutes for exposure/risk reconciliation.

The cursor is scoped to the Trade Manager, so profiles using that manager share its rotation. SQLite records the chosen target and advances the cursor in the same transaction before dispatch. Restart, Start/Pause and session rollover do not reset it. If a target set/order changes, the next selection follows the current saved order after the last account if that account is still present; otherwise it starts at the first ready account.

Only an actual selected target advances the cursor. If no account is ready, a durable blocked delivery outcome is recorded and no target is queued. Internally that outcome uses account ID zero as an unassigned marker; the dashboard displays “No account selected,” and zero is never passed to an execution engine or broker API.

A repeated delivery never advances the cursor again, gains additional targets, switches modes or replays. Copy and distribution use the same delivery claim boundary, preventing a mode/configuration change from expanding an already claimed delivery. Interrupted claims remain interrupted after restart. Account-level active-intent constraints and all placement/mutation latches are unchanged.

## No fallback after selection

Once selected, an account owns that delivery outcome. A risk/read failure, rejected order, ambiguous submission, permission change or full queue records its outcome for that account. The signal is never reassigned, retried through another account or copied to additional accounts. The next new eligible signal continues the rotation after the previously selected account.

Signals received while no account is ready, before Start or before arming remain blocked even if accounts become ready or automatically recover later. Automatic recovery never drains old blocked alerts.

## Examples

With A, B and C ready, successive new signals select A → B → C → A. If B is already occupied, the next selection after A is C. If all three are unavailable, that signal is blocked and the cursor stays where it was. If C was selected but its fresh risk check rejects the entry, that signal ends blocked on C; it is not sent to A.

Copy continues to target A, B and C for every signal. Their outcomes are independent and are not an atomic broker transaction. An accepted target is never undone because another target failed.

## Validation

Offline regressions cover persistent rotation, skipped accounts, no-ready outcomes, duplicate/cross-mode claims, concurrent claims, separate managers, selected-account failures, configuration persistence and dashboard rendering. No real broker credentials or live order submissions are used in these tests.

## Per-account maximum risk

The manager's `max_risk_per_trade` is the default maximum planned risk in USD. In multiple-account mode, `account_risk_overrides` can replace it for individual selected account IDs. An override is not capped by the default: for example, a $150 default with A=$50, B=$100 and C left blank uses $50/$100/$150 respectively. Amounts must be positive and finite; unknown/unselected accounts and duplicate form entries are rejected. The UI allows an optional amount beside each selected account; blank removes that override. Without account discovery, account ID=amount lines are accepted for a draft.

Both Copy and Distribution freeze a separate single-account risk policy before arming. Position size is recalculated from that account's effective budget, authoritative contract tick size/value, stop distance, fees, slippage and quantity caps. Identical signals can therefore produce different quantities. An amount below the planned cost of one contract blocks that account; it never rounds up or silently increases risk. Copy's other targets remain independent, and Distribution does not reroute after that rejection.

The full manager and override mapping remain in the frozen routing scope/job evidence; each execution captures its own effective account policy. Maps are deep-cloned so changes cannot leak into another account or an existing trade. Editing defaults/overrides pauses affected profiles and requires the existing fresh setup/Start checks. Old clients that omit override fields preserve saved overrides; explicit blank fields from the updated form clear them. Existing policies without overrides keep their previous default risk behavior.

This is planned per-trade risk, not guaranteed realized loss, automatic loss enforcement, portfolio protection or a provider MLL calculation. Per-trade-only policies continue to omit daily-loss/floor calculations. Actual slippage and losses can exceed the planned amount.

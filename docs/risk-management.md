# Risk management

New and saved Trade Managers use **per-trade risk only** (`per_trade_only: true`). Set the maximum planned loss in USD (default displayed value: $150), plus account routing. The editor no longer asks for daily loss, session trade counts, total open risk, cross-account risk or a default profit objective. Stop and target prices come from the signal or manual ticket.

Per-contract planned risk = stop ticks × authoritative tick value + estimated round-trip fees/commissions + reserved slippage ticks × tick value. Quantity = floor(maximum risk per trade / per-contract risk), capped by the execution maximum contracts. Quantities round down. Reject when one contract cannot fit. Manual exact quantities are rejected if too large; never silently resize them. Planned risk is an estimate: slippage, gaps and costs can cause actual losses above the selected amount.

A per-trade-only policy does **not** require a manual account floor and does **not** calculate or enforce Topstep MLL, liquidation distance, a cumulative daily loss limit or a trade-count limit. No artificial floor is inferred from account balance. The broker/platform still enforces its own restrictions; `canTrade=false` blocks entries. This is an explicit reduction in bot-level risk controls requested by the operator, not automatic MLL discovery.

Every entry requires a fresh eligible PRAC simulated account, verified session, authoritative contract metadata, current quote, valid stop/target, fee/slippage reserves, local Start/arming and a flat account without working orders. One active execution, durable intent/signal claims and protection checks remain mandatory. No adding to positions, averaging down, martingale, unprotected entry or multi-account execution is implemented. Scheduled flattening and emergency protection failure handling remain active.

## Existing profiles and recovery

Existing profiles lacking `per_trade_only` retain their original limits until explicitly saved in the simplified editor. Saving pauses execution and switches to per-trade-only; it never starts trading. Legacy profiles still require their verified fixed floor, session loss/trade limits and minimum of per-trade/per-signal/open-risk/session/floor budgets. Existing intents persist the policy used at submission, so editing a manager cannot remove controls from an active trade. Historical daily and protective lockouts are not erased; any existing session lockout still blocks entries until its session ends. Restart never restores Start or replays placement.

## Verification

Offline tests cover cost-aware sizing, stop-distance changes, invalid risk amounts, removal of hidden daily/trade/floor caps for simplified profiles, no-floor setup/start, retained legacy behavior, durable policy on intents, exact-quantity rejection, protection checks, pause, and session/DST behavior. No live order was submitted to test this change.

The signal-to-market deviation guard is optional through an authenticated execution-setting toggle. It defaults on; disabling it does not disable freshness, tick validation, stop/target direction, cost-aware sizing or any active-account/protection check. Zero ticks retains strict zero tolerance. Both Practice tickets and automatic webhook execution use the saved setting through the shared sizing engine. See [execution settings](manual-execution.md#optional-maximum-price-deviation).

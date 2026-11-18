# Automatic contract rollover

Implemented behavior: automation profiles have a **Contract selection** setting in Trading setup. Existing profiles remain in pinned-expiry mode. **Automatic rollover — ProjectX active expiry** is an explicit opt-in; enabling it changes the profile scope, pauses affected entries and requires the existing setup confirmation and Start once. Subsequent expiry changes retain that Start within the running process and do not require a manual restart.

## Authorization and selection

The selected reference contract identifies the authorized instrument. Its exact broker `symbolId` is obtained from authoritative discovery or a bounded read-only contract-by-ID lookup; it is never inferred from a TradingView alert, display name, expiry string, price or global allowlist. An inactive reference expiry can still identify the instrument. For example, the broker may identify MCL as `F.US.MCLE` even when alerts use the routing label `MCL`.

The profile retains the reference expiry and verified broker instrument in its saved scope. Automatic mode permits a different expiry only for that exact broker instrument. Pinned mode continues requiring the configured contract ID. Alert entry, SL and TP prices are still used directly; rollover does not convert prices between expiries or align an external chart automatically. Existing quote-deviation, tick and risk checks remain mandatory.

The runner consumes the existing ProjectX discovery snapshot, with a bounded freshness check. Exactly one contract must be marked active for the authorized broker instrument. Missing, stale, disconnected, malformed or ambiguous discovery blocks new entries and retains the running choice. Corrected discovery automatically allows another fresh arming attempt; there is no inferred calendar rollover or silent cross-instrument substitution. Discovery uses the existing shared client/request governor and is not a new health-check probe.

## Existing exposure and running continuity

Each account keeps its current execution contract while an intent or dispatch is active. Rollover does not close, transfer, cancel, replace or adopt an existing position or protective order. Pending entries retain their original captured expiry and normal lifetime/restrictions. Monitoring continues using the captured contract and linked SL/TP.

When that account has no active/unresolved intent, the runner can select the newly active expiry and perform the normal fresh account, flat/no-order, metadata, risk and session checks. It then rearms under the existing Start. The execution engine independently verifies that the resolved contract belongs to the authorized broker instrument and is active. Each intent permanently captures the actual selected contract and authoritative tick metadata.

Accounts run independently: an occupied account retains its old trade while a different flat account may become ready on the new expiry. One alert freezes one resolved expiry before its Copy/Distribution target jobs are claimed. Only accounts armed for that exact resolved expiry can dispatch it; accepted copies of one alert cannot independently choose different expiries.

If discovery changes before the replacement is armed, new alerts can be blocked during the transition. They are never held for execution later, replayed, or reassigned. The profile automatically becomes ready again after checks pass, but no uninterrupted-entry or fill guarantee is made.

## Recovery and controls

Technical recovery after an old-expiry trade closes may warm the newly selected expiry's quote only after verifying the same authorized broker instrument. It still requires the existing two fresh flat/no-order observations and fresh risk/account checks before rearming. Rollover never waives retained risk, ambiguous-write or unknown locks, alters account ownership, resets round-robin selection, or restores a Start after manual Pause, emergency Flatten, configuration revocation or process restart.

The current arming contract is displayed per account on Execution runners; history and target jobs retain the actual execution expiry. A sanitized rollover audit event records that fresh flat-account checks succeeded without logging credentials, tokens or raw provider responses.

Tests use simulated gateway observations to cover startup on an active replacement, continuous operation after closing an old trade, unchanged existing protection, stale/ambiguous/wrong-instrument discovery, recovery without another Start, manual Pause, and the independent engine instrument guard. No broker orders are placed to validate this feature.

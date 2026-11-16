# Project rules
- Run go test ./... and go vet ./... after each phase; no real credentials in tests.
- Default to read-only; never add an execution path without explicit arming, risk checks, reconciliation, and durable idempotency.
- All trading traffic originates on the user's personal laptop. No proxy, tunnel, cloud relay, or remote order trigger.
- TradingView defaults to local review. Automatic execution requires configured exact profile/account scope and an explicit local Start; it stays disabled otherwise. The operator is responsible for confirming provider permission; the application does not require a support ticket/reference. A user-started continuous runner may enter subsequent verified sessions only after fresh flat-account/risk reconciliation.
- Verify API behavior against official documentation; record uncertainty and fail closed.
- Accept verified one-way or reciprocal OCO sibling links; never infer linkage from prices alone. Preserve the first management trigger before emergency progress overwrites the display.
- Local repair of the legacy reciprocal-only OCO false alarm requires explicit password-confirmed review, matching closed one-contract history, two fresh flat/no-order observations, and an atomic scoped audit. Never clear daily locks/uncertain intents, arm, or replay as part of repair.
- Never retry ambiguous order placement. Missing protection blocks entries and invokes the documented fail-safe.
- A restart or reconnect does not establish flatness. Session/holiday uncertainty blocks execution. Per-trade-only policies intentionally do not calculate or enforce MLL; never represent this as verified floor protection.
- Keep credentials, tokens, response bodies, and signal payloads out of logs.
- Use context cancellation, bounded I/O, UTC timestamps and America/Chicago session rules.
- Document actual implemented behavior separately from planned phases.
- Stored Trade Manager policies are configuration, never proof of enforced limits; multi-account targets must never be silently reduced to one account.
- Existing administrator stores override bootstrap/environment credentials. Never restore revoked logins or log passwords, hashes, or CSRF seeds.
- Dashboard automation profiles are webhook-only. Practice API tests never require or create an automation profile. Retired manual JSON import routes cannot dispatch orders.
- Keep UI/risk bracket distances positive; encode signed wire price offsets (BUY stop negative/target positive; SELL stop positive/target negative). Never apply direction twice or retry an old rejected signal.
- Manual Market/Limit tickets share the execution engine; exact quantities must fit risk, never be silently resized. Preview is not submission.
- Automatic write support includes API-verified simulated Practice, Trading Combine and Express Funded accounts; Live Funded is unsupported by Topstep API. Manual Practice test tickets remain PRAC-only. Account identity for automation uses pinned numeric ID, not a name prefix, with verified ordinary-session hours (operator-confirmed session windows or the reviewed, bounded continuous calendar). Legacy policies require a known fixed floor; per-trade-only policies require no floor. Merged holiday sessions deny arming.
- Hold the process execution lease before workers/listeners. Recovery never restores arming or replays placement; emergency mutation latches must survive restart.
- Automatic deliveries are durably claimed once before dispatch. Never replay a claim after interruption; pre-arming/disarmed alerts must not fire later. Review-only profiles remain separate.

- Automatic execution is account-scoped: one running profile owns an account; distinct accounts run independently. Keep the SQLite one-active-intent-per-account constraint and never restore the global active-intent index.
- Freeze and atomically claim every target before fanout. Independent outcomes are not an atomic broker transaction; never undo accepted targets or replay blocked/interrupted ones.
- Unix datagrams carry wake hints only after inbox commit. SQLite remains authority; socket loss falls back to bounded scans. Never accept account, credentials or execution authority through a socket notification.
- Share JWT, contract subscriptions and request governor. Never cache fresh exposure/risk checks merely to reduce entry latency; preserve mutation budget under the documented API ceiling.

- Instrument scope belongs to each automation profile and its pinned contract. Do not restore a global instrument allowlist. Ignore legacy allowed_instruments YAML; manual Practice contracts must come from active authoritative discovery. Calendar/risk/protection checks remain mandatory.

- Trade Managers own recurring Chicago start/last-entry/flatten schedules. Do not use ticker names as calendar permissions. Intersect manager windows with verified authorization and reviewed holiday caps; never widen them or infer an unknown year's holidays. Schedule edits pause affected profiles and preserve existing intents' captured windows.

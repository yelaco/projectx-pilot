# Multi-profile automation and dispatch update

Implemented November 16, 2026. See [architecture and diagram](architecture.md) and [configuration/update instructions](automatic-execution.md).

## Delivered

- Independent account workers and execution engines; concurrent profiles on distinct accounts, complete multi-account Trade Manager fanout, atomic ownership checks, and one durable active intent per account.
- Separate persisted profile trading setups, migration of valid old single-profile configuration, preserved credentials/tokens/URLs and startup paused. Manual Practice tests remain separate.
- Atomic frozen target claims before scheduling; deterministic account-specific IDs; independent submitted/blocked/uncertain/interrupted outcomes; no duplicate, crash, configuration-change or pre-arming replay.
- Immediate payload-free Unix datagram notification after inbox commit, bounded/coalesced wakes and a 30-second lost-wake scan. Included in the existing installer/services without extra setup.
- Shared JWT/client, HTTP connections, bounded/singleflight metadata cache and reference-counted warm quotes. Concurrent fresh preflight reads with account-scoped execution serialization.
- Shared user SignalR hub, subscription acknowledgement tracking, account events, reconnection/subscription restoration and full REST reconciliation wake. Adaptive REST backstop and a shared cancellable API governor with mutation reserve.
- Persistent global entry pause/resume, profile/account pause and confirmed flatten controls, account runner page, individual delivery results, safe live dashboard updates and measured arrival-to-acknowledgement timing.
- Account-specific risk/session/news/operation-day enforcement, protection and emergency latches. Pause cancels uncommitted preflight; a durable submission attempt still completes once and reconciles.

## Verification

Run `go test ./...`, `go test -race ./...`, `go vet ./...` and `node tests/dashboard-forms.test.cjs`. Both native binaries build with `go build -buildvcs=false`. The installer passes `bash -n`.

Regression coverage includes blocked-account concurrency, multiple profiles, concurrent ownership conflicts, whole-scope HTML setup/permissions, independent setup persistence, duplicate/frozen fanout, global pause persistence, interrupted recovery beyond display limits, configuration loading without consuming another process's claims, corrupted-account recovery rejection, paused preflight cancellation, isolated protective failure/emergency closing, per-account SQLite reservation migration, cache coalescing/expiry, quote reference counting, socket shutdown/permissions, user stream restoration, read-rate/mutation reserve and CSRF/origin/password controls.

The end-to-end offline test drives the real HTTP receiver, SQLite commit, Unix socket wake, manager dispatcher and two account engines to a mocked gateway. One observed run completed both mocked acceptances in approximately 1.7 ms. This only demonstrates removal of local polling delay; it is not a real TradingView/network/ProjectX latency claim or an SLA.

A separately launched read-only binary was checked through authenticated loopback HTTP (`/runners`, `/api/monitor`), with credentials unset, no arming/runners, private 0600 socket creation and graceful SIGTERM/socket cleanup. No installed systemd unit was changed, browser visual inspection was unavailable, and no trading/account activation request was sent.

## Explicit boundaries

One running profile per account; at most 20 accounts per Trade Manager. Multiple targets execute independently and can partially succeed; simultaneous fills are not guaranteed. Multi-account policies require per-trade-only risk, independently per account with no aggregate portfolio cap or MLL calculation. Legacy fixed floors remain single-account. API eligibility includes simulated Practice/Combine/Express Funded, not Live Funded. Continuous calendar coverage remains reviewed 2026 MNQ/MGC, with unknown/merged sessions denied. API budgets, network outages, provider permission and server-side brackets still constrain execution. Restart remains paused; installation never authorizes trading. These boundaries are described on the dashboard/setup pages, not silently bypassed.

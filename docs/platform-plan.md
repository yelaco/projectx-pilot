# ProjectX Yelaco platform plan

Status: planning only. Recorded 2026-11-17. This document captures product direction and constraints for later implementation; it does not authorize implementation, deployment, trading, repository renaming or changes to the running service.

## Identity and current implementation

The intended platform/project name is **ProjectX Yelaco**, with the intended repository name **projectx-yelaco**. **Pilot** remains the execution engine within the platform. The current repository is still `projectx-pilot`; renaming the repository, packages, services and deployment paths is deferred.

Pilot already provides webhook automation, account-scoped execution, risk/session checks, monitoring, local persistence, manual Practice tickets and an optional authenticated remote dashboard. The platform capabilities below are planned additions, not claims about existing behavior. Refer to [architecture](architecture.md), [execution](automatic-execution.md) and [remote dashboard](remote-dashboard.md) for implemented behavior.

## Product scope

Build one workspace for research, execution and review:

| Area | Planned capabilities |
| --- | --- |
| Trading workspace | Charts, indicators, signals, order tickets, positions and explicit order/protection management |
| Strategy lab | Algorithm code, parameters, immutable versions and research results |
| Backtesting | Historical simulation, realistic costs/fills, reproducible runs and out-of-sample evaluation |
| Algo manager | Strategy deployment, observation/paper/live modes, account and instrument assignment, recurring windows and sessions |
| Webhook automations | External alert intake, source authentication, profile routing, scheduling, Start/Pause and delivery outcomes |
| Analytics | Performance by source, strategy/version, account, instrument and session; latency, slippage, rejects and expected-versus-actual fills |
| Journal | Chart-linked trade reviews, notes, tags, screenshots and automatically captured execution context |

Personal use is the initial cost/design baseline. Whether to support other traders, multiple independent users or commercial distribution remains undecided; do not assume shared-account access or market-data redistribution rights.

## Keep algorithms and webhook automations separate

These are distinct product modules and operational lifecycles:

| Concern | Algorithmic strategies | Webhook automations |
| --- | --- | --- |
| Decision source | Strategy code running in the platform | External alerts such as TradingView |
| Configuration | Code version, parameters, required data and schedule | Endpoint, credentials, strategy/instrument mapping and schedule |
| Research | Backtests, market replay and paper/shadow comparisons | Alert history and delivery/execution analysis |
| Controls | Run/Pause and explicit version deployment | Start/Pause and credential rotation |
| Health | Runtime state, data readiness and decision generation | Receiver readiness, incoming alerts and delivery outcomes |

Existing TradingView profiles belong under Webhook Automations. Do not silently turn them into locally running algorithms or replay old alerts when a source is enabled.

Both sources submit proposals to Pilot. Manual order tickets should also use Pilot's execution engine. Every trade/event should retain its origin and exact identifiers: algorithm/version/parameters, webhook profile/alert/delivery, or manual action. Analytics may compare these sources without mixing their identities.

## Architecture direction

Start with one repository and clear component boundaries. Separate processes do not require separate repositories. A conceptual structure is:

```text
platform web interface
  + strategy research/runtime and backtest workers
  + webhook automation management
  + analytics and journal
          |
          v
       Pilot
  risk, account ownership, reconciliation,
  durable claims, broker execution and monitoring
          |
          v
  direct ProjectX connection on the execution device
```

No directory migration, framework selection or engine rewrite has been approved. Preserve existing Go execution code and SQLite state where appropriate. Split repositories later only if independent ownership, consumers or release cycles justify it.

Strategy code must not call broker mutation endpoints directly. Pilot remains the execution authority and must preserve exact account/contract scope, explicit arming, risk/session/protection checks, durable idempotency and reconciliation. Existing one-owner/active-intent constraints remain until a separately reviewed design changes them. Concurrent algorithms and webhook sources targeting one account need an explicit conflict policy; do not silently net, queue, reverse or merge their proposals.

A strategy's research and live operation should share decision logic where practical, while simulation and broker execution remain distinct. Pin strategy code, parameters, data provenance and execution configuration so results and live trades are traceable.

Pausing/disabling a source stops new entries. Monitoring faults, schedules and data problems must not automatically close established positions or cancel their protection. Any future strategy-managed exits or adaptive SL/TP behavior requires a separately approved design consistent with operator control; it is not implied by adding algorithms or chart controls.

## Performance and reliability

Pilot needs predictable response time; charts need fresh data; research needs throughput. Isolate research from execution and cap worker concurrency, CPU and memory. Separate processes still compete for the same hardware. Do not let backfills, optimization or journal exports exhaust broker-request capacity, storage or memory needed for execution and emergency controls.

Share authenticated sessions, market subscriptions and applicable indicator calculations. Use realtime events for live updates, incremental historical downloads and local cached data for repeated backtests. Cached research data must never replace fresh entry exposure/risk reconciliation.

Retain durable event/order history, bounded safe-read retries, visible data gaps and explicit running/waiting/blocked/disconnected/unknown states. Never retry ambiguous placements or automatically replay/rearm after recovery. Isolate faulty strategy runtimes, maintain backups and test restoration. A restart or historical exposure record never proves current flatness or protection.

The execution device must be awake, powered, connected and running the required services. Broker-held SL/TP is independent of the local platform, but monitoring and remote controls can be unavailable during an outage. Authentication and process health alone do not establish data freshness or safe execution.

## ProjectX-only, zero-additional-cost target

Use **Topstep's ProjectX API as the sole planned broker and market-data provider**. Avoid required paid chart platforms, external historical-data subscriptions, hosted databases, cloud compute or paid analytics services. Prefer open-source UI/charting and local storage/compute; choose actual libraries later after checking licensing and maintenance.

The budget target is **zero additional recurring platform subscriptions**, beyond existing Topstep/ProjectX access, domain, hardware, electricity and internet. This is not a promise of zero total cost: storage growth, backup hardware and development/maintenance still consume resources. Do not introduce a paid dependency without an explicit budget decision.

Topstep's API access page currently lists $29/month, or $14.50/month with its advertised Topstep discount. Treat this as a dated reference, not a guaranteed future price or an assertion about the user's billing. [Topstep API access and billing](https://help.topstep.com/en/articles/11187768-topstepx-api-access)

| Component | Initial approach |
| --- | --- |
| Hosting and execution | Existing user-selected personal execution device; no separate hosted engine |
| Charts | Open-source rendering with ProjectX data |
| Historical data | Incremental local cache, reusable across charts and research |
| Backtests | Resource-limited local workers |
| Journal/analytics | Local databases/files and derived summaries |
| Remote viewing | Existing Cloudflare setup within its applicable plan/limits |

The documented history endpoint supports aggregated bars and up to **20,000 bars per request**. Its published limit is **50 requests per 30 seconds**; other endpoints have a published limit of **200 requests per 60 seconds**. Recheck limits before implementing and preserve the existing shared request governor and execution budget. [Historical bars](https://gateway.docs.projectx.com/docs/api-reference/market-data/retrieve-bars/), [rate limits](https://gateway.docs.projectx.com/docs/getting-started/rate-limits/)

ProjectX documents realtime account/order/position and market events through its realtime hubs. These are the proposed live source, not evidence of unlimited historical tick/depth availability. [Realtime API](https://gateway.docs.projectx.com/docs/realtime/)

Verify actual historical coverage, expired-contract availability, aggregation/session semantics, gaps and permitted storage/use for the subscription. Do not promise unlimited history, continuous-contract back-adjustment or historical order-book replay. Any continuous series needs documented rollover rules and contract provenance; it must not silently change Pilot's pinned execution contract.

Start by evaluating bar-based research; minute versus second/tick granularity remains undecided. Bar-only simulations cannot prove intrabar fill sequence when both SL and TP are touched. Specify conservative/explicit ambiguity handling, commissions, slippage, limit-fill assumptions, Chicago sessions and futures expirations. Guard against look-ahead bias. Recording permitted live data may build future research coverage, but does not recreate unavailable past data.

## Provider-policy question requiring resolution

Topstep's current API page restricts order flow and automated order triggers through remote servers; it separately describes supporting read-only dashboards, historical storage and research. Direct device-to-broker traffic does not by itself establish permission for remote controls or webhook-triggered execution. [Provider wording](https://help.topstep.com/en/articles/11187768-topstepx-api-access)

Earlier discussion treated authenticated remote controls plus direct broker egress as sufficient. That assumption is not established by the current published wording. Verify how these restrictions apply to the exact personal-device, Cloudflare and remote-control workflow before treating it as permitted or expanding it. Existing technical capability and successful authentication are not provider permission.

This planning note does not change the running deployment or claim that permission has been obtained. Review [existing provider-policy documentation](topstep-compliance.md) alongside the current official terms; document any resolved interpretation rather than silently assuming approval.

## Proposed sequence and useful enhancements

1. Define common proposals/events, source identity and versioned configuration; measure the current execution baseline.
2. Add the chart workspace and automatically populated journal using existing execution records.
3. Verify data coverage and build the historical cache, reproducible backtests and explicit fill model.
4. Add isolated strategy runtimes and observation/paper modes, then an explicit research → backtest → paper → live deployment workflow.
5. Add algo scheduling/conflict handling and richer portfolio/execution analysis.

Potential later additions: historical market replay, walk-forward/parameter-stability analysis, chart snapshots, a unified event timeline, configuration-change history and data-feed/contract-roll health. A portfolio overview may display aggregate exposure; do not label it an enforced portfolio risk limit until that enforcement actually exists.

Before implementation, settle: personal versus multi-user scope; strategy language/interface; initial instruments and data granularity; historical coverage; account/source conflict behavior; acceptable hardware workload and backup plan; chart/data licensing; and provider-policy applicability. No implementation timeline or paid infrastructure is committed.

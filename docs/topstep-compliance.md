# Topstep compliance and execution boundaries

The official policy reference is [TopstepX API access](https://help.topstep.com/en/articles/11187768-topstepx-api-access). Local order execution does not by itself establish that cloud-generated alerts or tunnel delivery are permitted. The operator must confirm the intended workflow with Topstep.

At the operator’s request, the application no longer asks for or validates a support approval ticket/reference. Existing saved reference fields are legacy data and are ignored. This change removes a software prerequisite; it does not obtain provider permission or change Topstep’s rules.

Read-only remains the default. Automatic execution requires exact profile/account/Trade Manager configuration, a pinned contract, one-time Practice/calendar setup and explicit local Start. TradingView defaults to review-only. All trading API calls originate directly from the laptop; trading transports ignore HTTP(S) proxy environment variables and refuse redirects. The separate optional receiver persists notifications and holds no trading credentials. Its local dispatcher can submit eligible alerts only while the selected automatic profile is running.

Automatic profiles support API-verified simulated Practice, Trading Combine and Express Funded accounts. Live Funded accounts are excluded by Topstep’s API policy. Manual Practice tests remain PRAC-only. Simplified Trade Managers do not calculate/enforce MLL or require a floor; Topstep still enforces its account restrictions. Legacy policies require a known fixed floor. Unsupported calendars and merged-session accounting block execution. Risk checks, fresh account reconciliation, durable signal/order claims and protective brackets remain mandatory. No blind placement retries or delayed replay of disarmed alerts are permitted.

Start can remain active across reviewed sessions, with fresh reconciliation before each session. Closures and risk lockouts block entries. Pause, critical failures and process restart clear execution permission; monitoring and emergency management remain available. This cannot verify OS-level VPN, routing, device location or provider permission.

See [automatic execution](automatic-execution.md) for actual supported behavior and calendar bounds. No live activation or orders were sent during development.

2026-11-14 review: official API policy still requires personal-device order flow
and prohibits remote-server automated triggers reaching order endpoints. Trade
exit alerts are order flow too; local API requests alone do not establish remote
trigger compliance. The application does not resolve or certify permission for
this workflow. No service was armed/deployed while implementing exit alerts.

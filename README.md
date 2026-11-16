# ProjectX Pilot

Local trading automation and account management for ProjectX Gateway.

Connect TradingView alerts to account-specific execution runners, manage risk policies, and monitor orders from a local web dashboard. Built with Go and SQLite, with native systemd services for Arch Linux.

## Features

- **Automation profiles** — webhook URLs, delivery tokens, and BUY/SELL message templates for each strategy.
- **Multiple accounts** — route one signal to several accounts, or run separate profiles on different accounts.
- **Trade managers** — calculate whole-contract position sizes from stop distance, planned risk, fees, and slippage.
- **Execution monitoring** — track orders, positions, protection, delivery outcomes, logs, and acceptance latency.
- **Trading schedules** — general and per-manager operation days, session cutoffs, and US FOMC filtering.
- **Local controls** — pause entries or request emergency flattening globally, per profile, or per account.

## Getting started

On your Arch Linux trading computer, run as your regular user:

```sh
git clone https://github.com/yelaco/projectx-pilot.git
cd projectx-pilot
./scripts/install-user-service.sh
```

The installer checks dependencies, builds the application, and starts its user services. It prints your initial login details. Package installation may require sudo.

Open **http://127.0.0.1:8080**. The standalone **[Guide](http://127.0.0.1:8080/guide)** includes a quick start and detailed operating instructions; you can also open it from the dashboard’s **ⓘ Guide** button.

To configure your first automation:

1. Configure your ProjectX connection and discover your accounts.
2. Create a Trade Manager and an automation profile. Configure its routing, trading setup, and webhook messages.
3. Enable webhook receiving, configure delivery, and click **Start automation** when ready. Check **Execution runners** for each account's readiness.

The application starts **read-only and disarmed**. Receiving an alert alone does not authorize trading. See the [automatic execution guide](docs/automatic-execution.md) for the complete setup.

## Supported scope

Automatic execution supports eligible simulated Practice, Trading Combine, and Express Funded accounts. Manual API test tickets are Practice-only; Live Funded accounts are unsupported.

One running profile owns each account, with up to 20 targets per Trade Manager. Risk applies independently per account; fanout can partially succeed. Per-trade-only policies do not enforce Topstep MLL or an aggregate portfolio cap. Trade Managers define trading windows; reviewed **2026 holiday cutoffs** can shorten them. Confirm that the selected window fits your products’ hours and pauses. Restarts leave automation paused.

## Documentation

| Guide | What you'll find |
| --- | --- |
| [Automation setup](docs/automatic-execution.md) | Profiles, account runners, routing, and updates |
| [Dashboard](docs/admin-guide.md) | Configuration, monitoring, and administrator access |
| [Webhook delivery](docs/named-tunnel-setup.md) | Permanent HTTPS hostname and tunnel setup |
| [Risk and recovery](docs/risk-management.md) · [Recovery procedures](docs/recovery-procedures.md) | Sizing, protection, failure handling, and restart behavior |
| [Linux deployment](docs/deployment-archlinux.md) | Services, logs, upgrades, and laptop operation |
| [Architecture](docs/architecture.md) · [Development](docs/development.md) | System design, builds, and tests |

ProjectX Pilot is an independent community project, unaffiliated with ProjectX or Topstep. Local execution does not establish permission for a remote-signal workflow; review the [provider-policy boundaries](docs/topstep-compliance.md) before enabling it.

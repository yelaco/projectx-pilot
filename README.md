# TopstepX local trading application

Native Go local administration, notification inbox, and **manual or automatic Practice-account market/limit execution**. Defaults remain read-only and disarmed. Practice execution reaches ProjectX; it is not paper mode. No trading request was sent during development.

Open **Practice execution** (`/execution`) after updating. Configure one Practice account and single-account Trade Manager, grant account eligibility, verify Auto OCO/session hours, and arm. Enter Buy/Sell, exact contract count, stop/target ticks and a limit price when appropriate. **Market Order** and **Limit Order** calculate a preview; a separate password-confirmed approval submits. See the [step-by-step execution guide](docs/manual-execution.md).

Account API data does not identify Practice specifically or expose MLL. Simplified Trade Managers use maximum planned risk per trade only: no manual account floor, MLL calculation, daily-loss or trade-count cap. Existing managers keep legacy limits until saved. Session/holiday checks remain; merged holiday sessions are unsupported. Webhook alerts default to review. Automatic TradingView routing is implemented behind exact-profile configuration and explicit local Start; it has not been enabled here. Multi-account execution and paper simulation remain unavailable.

For automatic alerts, use **Automation → Automatic execution** to pin the contract/order type, complete its one-time Trading setup, then use the single Start/Pause control. Start remains active across verified session boundaries while the process runs. Practice execution is for manual tickets. [Automatic execution setup](docs/automatic-execution.md).

## One setup command

```sh
./scripts/install-user-service.sh
```

On Arch this installs missing build/tunnel packages (sudo for packages only), runs checks, builds and starts the native user services, preserves existing settings and opens the admin page when possible. It prints the local admin password. Before the first upgrade from the old manual workflow, stop your manually launched receiver/tunnel terminals once; the installer does not kill unrelated processes.

Open **http://127.0.0.1:8080**, username **local**. Configure your ProjectX connection, Practice account and automation profiles in the browser. Use **Settings → Webhook & tunnel → Use Cloudflare preset** to enable inbox receiving and the managed test tunnel together. No manually generated/exported webhook token is required. Full per-profile webhook URLs are shown in the automation list and profile detail. Change/add/revoke administrator logins in **Settings → Admin access**. Reusable **Trade managers** configure risk/session limits and single/multiple-account routing. Single-account limits are enforced in the manual Practice workflow; multi-account execution remains unavailable. Each TradingView profile gets a private token, profile URL and BUY/SELL template drafts; rotate credentials and configure verified indicator price placeholders on the page. Unknown Swift Algo stop/target fields remain explicit mapping requirements rather than guessed values.

The dashboard has focused Overview, Automations, Trade managers, Inbox, Accounts, Activity and Settings pages, with profile-specific tabs for credentials and messages. It is also a monitor: receiver/tunnel/connection status, uptime, queue counts and persistent sanitized activity update every ten seconds. Filter/search the activity feed and export a monitor snapshot. A dedicated webhook/tunnel running log on the connection settings page updates every three seconds and supports pause, component filtering and search. Config forms remain untouched. Review-only alerts cannot submit orders. A configured, locally started automatic profile can submit new alerts; its outcomes appear in Inbox and Practice execution.

See the [admin and monitoring guide](docs/admin-guide.md) for the complete workflow, migration, template mappings, monitor limits and Quick Tunnel URL changes. The [webhook guide](docs/webhook-receiver.md) contains protocol details and legacy manual startup instructions.

For a developer-only run, build `cmd/bot` and `cmd/webhook` with CGO and system SQLite headers. Normal installed use relies on the two managed user services. An example config is in `config/example.yaml`; private runtime state includes credentials, admin password, per-profile ingress settings, signal/delivery databases and bounded activity databases. File modes are 0600 in a 0700 directory. No credentials are encrypted at rest. Environment credentials remain optional overrides.

## Verification

```sh
go test ./...
go test -race ./...
go vet ./...
# Optional browser-script form regression checks (Node.js):
node tests/dashboard-forms.test.cjs
# Explicit opt-in, authenticated read-only integration check:
PROJECTX_INTEGRATION=1 go test ./tests -run TestProjectXReadOnly -v
```

No ordinary test needs credentials or network access. Tests use an injected HTTP transport and simulated REST failures. In restricted workspaces set `GOCACHE=/tmp/projectx-go-cache`. The build command disables VCS stamping because this workspace exposes an empty, read-only `.git` directory; normal Git checkouts can omit that flag. Runtime credentials are never needed for compilation. The integration check sends authentication/account/contract requests only; ordinary tests skip it.

## Implemented scope

Authentication/renewal, account/contract discovery, local administration, notification receiving/tunnel management, credentials, signal review/dedup, single-account risk sizing, market quote subscription, explicit arming/approval, durable market/limit placement, partial-fill/protective verification, automatic webhook dispatch with durable receipts, active REST reconciliation, emergency management and restart recovery are implemented and mock-tested.

This does not meet every original production acceptance criterion: user-hub streaming, automatic MLL/calendar inputs, paper simulation, multi-account trading remain unavailable. Protection depends on reachable ProjectX and server-side Auto OCO; read the [recovery limits](docs/recovery-procedures.md) before testing. No authenticated write integration test has been run.

See [architecture](docs/architecture.md), [API evidence](docs/projectx-api.md), [compliance](docs/topstep-compliance.md), and [deployment](docs/deployment-archlinux.md).

For a permanent webhook hostname on this or another Arch laptop, use:

```sh
./scripts/install-user-service.sh --named-tunnel alerts.yelaco.trade
```

Paste the named tunnel token at the hidden prompt. Cloudflare domain/tunnel/route creation is one-time; the installer saves the URL, disables Quick Tunnel, and starts a non-root named connector. Stop the old connector before switching machines; profiles/credentials still need local configuration and trading starts paused. See [named tunnel setup](docs/named-tunnel-setup.md).

Automation profiles now accept webhook signals only in the dashboard. Practice execution is a separate API order test with no automation profile requirement: choose the saved Practice account/Trade Manager, an allowed active contract, quantity and stop/target ticks. Preview never transmits an order; approval submits through the same risk/protection/idempotency engine. Dashboard manual JSON signal import is retired; stored review history remains readable.

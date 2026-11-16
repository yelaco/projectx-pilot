# Development

The Go module is `github.com/yelaco/projectx-pilot`. Development requires Go 1.23 or newer, a CGO-enabled build, GCC, pkgconf, and SQLite headers/shared libraries. Node.js is needed only for the dashboard script tests.

On Arch Linux:

```sh
sudo pacman -S --needed go gcc pkgconf sqlite nodejs
```

## Build and check

From the repository root:

```sh
go build -buildvcs=false -o bin/bot ./cmd/bot
go build -buildvcs=false -o bin/webhook ./cmd/webhook
go test ./...
go test -race ./...
go vet ./...
node tests/dashboard-forms.test.cjs
```

The installer disables VCS stamping; normal Git checkouts can omit `-buildvcs=false`. Ordinary tests use mocked gateways and local HTTP/WebSocket servers. They require no trading credentials and submit no real orders. Mock timing is not a measurement of live TradingView or ProjectX latency.

An authenticated, read-only integration check is available only through explicit opt-in:

```sh
PROJECTX_INTEGRATION=1 go test ./tests -run TestProjectXReadOnly -v
```

This check uses configured ProjectX environment credentials and sends authentication, account, and contract discovery requests. It does not place orders. See `.env.example` for variable names; never commit populated environment files.

## Local development

Start with [config/example.yaml](../config/example.yaml), which defaults to read-only. The installed workflow uses the systemd services; advanced manual startup is documented in the [webhook guide](webhook-receiver.md). Keep the dashboard on loopback and use private state directories.

Runtime state contains credentials, administrator data, profile settings, execution/delivery databases, and audit history. Files use mode 0600 in a 0700 directory. Credentials are not encrypted at rest. Build and test commands do not need runtime state or credentials.

Read [AGENTS.md](../AGENTS.md) for project invariants and [architecture](architecture.md) for the execution and recovery boundaries. Changes to order submission must preserve explicit authorization, risk checks, durable idempotency, reconciliation, and no blind retries after ambiguous placement.

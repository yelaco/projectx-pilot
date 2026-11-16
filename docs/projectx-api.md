# ProjectX API verification

Reviewed official documentation on 2026-11-13. No authenticated calls were made in development. The machine-readable [TopstepX Swagger schema](https://api.topstepx.com/swagger/v1/swagger.json) and official Gateway pages were accessible. Do not infer unreviewed API capabilities.

Implemented POST endpoints:

| Endpoint | Request | Response |
| --- | --- | --- |
| `/api/Auth/loginKey` | `userName`, `apiKey` | `token`, success/error code |
| `/api/Auth/validate` | bearer token, empty object | `newToken`, success/error code |
| `/api/Account/search` | `onlyActiveAccounts:true` | `accounts`: id, name, balance, canTrade, isVisible, simulated (nullable locally to detect omission) |
| `/api/Contract/available` | `live:false` | contracts with ID, name, description, tickSize, tickValue, activeContract, symbolId |
| `/api/Contract/search` | `live:false`, `searchText` | up to 20 matching contracts |
| `/api/Contract/searchById` | `contractId` | contract metadata |

| `/api/Order/searchOpen` | `accountId` | working orders, partial-fill quantities and parent/OCO linkage |
| `/api/Position/searchOpen` | `accountId` | current positions |
| `/api/Trade/search` | `accountId`, UTC `startTimestamp`, `endTimestamp` | execution legs including nullable P&L, commissions, fees and voided flag |

Exposure/history methods reject absent arrays and observations belonging to a different account. The separate execution reconciler uses these methods; the discovery snapshot remains independent. Empty explicit arrays are valid observations; they do not establish streaming freshness. Trade history is not yet a complete session ledger. `simulated:true` does not identify a Practice account specifically.

Success requires `success:true` and zero errorCode; HTTP 200 alone is insufficient. Error strings contain only endpoint/status/code; upstream bodies and messages are omitted. Tokens have a documented 24-hour lifetime; this client attempts renewal after 23 hours and falls back to login if validation returns HTTP 401. Other renewal failures remain errors. HTTP 401 on discovery invalidates the token without replaying the request. No read retry loop is hidden in the client.

The documented REST origin is https://api.topstepx.com. The documented hub URLs are https://rtc.topstepx.com/hubs/user and https://rtc.topstepx.com/hubs/market. The documented market-quote SignalR JSON subset is implemented over Gorilla WebSocket; user-hub streaming is not implemented. `canTrade` is informational, not sufficient risk authorization. Account discovery does not establish MLL, liquidation distance, realized P&L or flatness. Contract tick metadata is authoritative input for future risk sizing; no instrument tick constants are embedded.

Sources:

- https://gateway.docs.projectx.com/docs/getting-started/authenticate/authenticate-api-key/
- https://gateway.docs.projectx.com/docs/getting-started/validate-session/
- https://gateway.docs.projectx.com/docs/getting-started/connection-urls/
- https://gateway.docs.projectx.com/docs/api-reference/account/search-accounts/
- https://gateway.docs.projectx.com/docs/api-reference/market-data/available-contracts/
- https://gateway.docs.projectx.com/docs/api-reference/market-data/search-contracts/
- https://gateway.docs.projectx.com/docs/api-reference/market-data/search-contracts-by-id/

Order placement was researched against the official [place-order guide](https://gateway.docs.projectx.com/docs/api-reference/order/order-place/) and Swagger: `customTag` must be unique per account, UI/risk tick distances are positive integers and outgoing bracket offsets are signed, and Auto OCO mode must be enabled on the platform. Submission acknowledgement does not establish fill or protection. Write endpoints remain excluded from the read-only client allowlist. A separate pinned `Trading` capability implements `/api/Order/place`, `/api/Order/cancel`, and `/api/Position/closeContract`; the execution engine provides arming, risk checks, durable reconciliation and protective monitoring. `/api/Order/search` is used for history. Market (2) and limit (1) requests carry signed stop (4) and limit-target (1) offsets relative to entry. Order modification and other entry types remain unavailable. No test transmitted a trading request.

Account-specific observation: the user successfully authenticated using their email as the platform login. The client accepts that value unchanged; dashboard guidance now asks for the exact platform login rather than forbidding email-shaped usernames. This observed behavior differs from the generic wording in the official login guide.

Protocol references: [ProjectX real-time events](https://gateway.docs.projectx.com/docs/realtime/), [Microsoft SignalR hub protocol](https://github.com/dotnet/aspnetcore/blob/main/src/SignalR/docs/specs/HubProtocol.md), and [Gorilla WebSocket](https://github.com/gorilla/websocket). The Go transport uses direct WebSockets with Bearer header authentication, documented quote subscriptions and record-separated JSON framing. Tests simulate frames and REST responses; compatibility with this user's live account has not been verified.

The automatic dispatcher uses exactly the existing pinned trading capability; it introduces no undocumented ProjectX endpoint. Mock end-to-end tests drive webhook → SQLite → manager/dispatcher → real risk/execution engine → typed mock gateway request and protective-order observations. No actual order request was sent to ProjectX.

## First-quote readiness and market failures

The market subset was checked against the official [real-time reference](https://gateway.docs.projectx.com/docs/realtime/): direct WebSocket market hub, SignalR JSON handshake, `SubscribeContractQuotes(contractId)` and `GatewayQuote(contractId, data)` with `lastPrice` and provider timestamps. Native WebSocket authorization uses the Bearer header, not a token-bearing URL. A successful REST account request does not prove market-data connectivity or entitlement.

Entry sizing and the Practice market test now wait up to four seconds for an initial fresh quote using an event-driven feed notification. They still reject quotes older than five seconds; no historical bars, stale quotes or local receipt timestamps are substituted. Active position monitoring remains nonblocking and fail-closed. Authentication, HTTP WebSocket rejection, handshake, subscription rejection, connection loss and stale data produce fixed diagnostic text; provider bodies/errors and JWTs are never exposed. Each subscription generation rejects callbacks from replaced streams. Reconnection clears quote permission and restores the subscription.

Offline WebSocket tests cover the actual handshake/subscription/first-quote path, bounded waits, old-stream callbacks, stale quotes, disconnect clearing and sanitized HTTP 401/subscription errors. No live credentials or trading requests were used. Closed markets, inactive expiries and missing market-data access can still prevent fresh quotes; the app cannot manufacture those data.

## Position Brackets rejection diagnosis

The [official placement reference](https://gateway.docs.projectx.com/docs/api-reference/order/order-place/) documents rejection code 2 when attached bracket objects are sent while the account uses Position Brackets. The rejected order can still receive an order ID. This app requires protected orders, so the remedy is to switch the exact selected account to Auto OCO Brackets in TopstepX Settings → Risk Settings while flat with no working orders. It never retries without brackets.

The client now recognizes only the exact documented Position Brackets error message and retains an `AutoOCORequired` boolean; the raw message/body is discarded. The execution monitor can therefore show a fixed actionable explanation in addition to HTTP/API codes. Code 2 by itself is generic rejection and is not enough to assign this cause. Unrecognized messages remain numeric-only. This affects diagnostics, not reconciliation, retry or authorization behavior. Previously stored numeric-only failures cannot recover their discarded reason.

## Signed bracket offset correction

Account-specific live API evidence supplied by the operator on 2026-11-13: `Invalid stop loss ticks (40). Ticks should be less than zero when longing.` This disproves our original assumption that the wire accepts positive distances for both legs. The generic official placement example describes tick counts without fully explaining direction and shows positive values; it must not be treated as proof that a BUY stop can be positive.

The risk plan and UI continue to use positive distances. At the final request boundary those distances are encoded as price offsets: BUY stop negative / target positive; SELL stop positive / target negative. For 40/80 input ticks these are `-40/+80` and `+40/-80` respectively. BUY stop negativity is confirmed by the supplied live provider rejection; remaining offsets follow directional price geometry and are tested offline, not claimed as live acceptance verification for every side/leg. No endpoint or protection policy changed. The typed client rejects zero or wrong-side offsets before transmission. Fill protection verification still uses positive planned distances and actual stop/target prices.

Tests now inspect outgoing JSON for BUY/SELL × Market/Limit, reject incorrect signs before network calls, and model mock broker children as `entry + signedTicks*tickSize`. The prior simulator applied direction to already-positive distances and therefore hid this wire-format bug. Webhook BUY/SELL end-to-end protection tests use the corrected model. No live order was placed during this correction, and previously rejected signals are never replayed.

## Shared multi-account connectivity (November 16, 2026)

The official [real-time reference](https://gateway.docs.projectx.com/docs/realtime/) documents `SubscribeAccounts()`, `SubscribeOrders(accountId)`, `SubscribePositions(accountId)`, `SubscribeTrades(accountId)`, and `GatewayUserAccount/Order/Position/Trade` events. One shared user connection now subscribes to all known runner accounts, waits for invocation completions, restores subscriptions on reconnect, and wakes full authoritative REST reconciliation after reconnect. Stream events are hints, never proof that missed events do not exist. The narrow SignalR JSON protocol is tested using actual local WebSocket handshakes, acknowledgements, account event routing, reconnection and restored subscriptions. Live provider connectivity was not exercised in development.

The official [rate limits](https://gateway.docs.projectx.com/docs/getting-started/rate-limits/) specify 200 requests/60 seconds for endpoints used here (historical bar retrieval has a separate limit and is not used). All production discovery/execution clients share one rolling governor: 180 total, 140 reads, eight in flight, leaving mutation capacity. Request-budget waiting is included in measured submission time. This is local accounting, not knowledge of external consumers of the same credentials.

Account workers share JWT renewal, HTTP connection reuse, a bounded one-minute contract metadata cache with singleflight, and one reference-counted quote subscription per contract. Exposure/account checks are uncached and run concurrently before entry. No undocumented batch/fanout API is assumed: one request is made per eligible account and independent failures are possible.

## Trading hours and manager schedules (November 16, 2026)

The verified contract schema supplies identifiers, active status, tick size/value and symbol metadata; it does not provide a documented authoritative hours/holiday field. Pilot does not invent one. Trading windows are selected on Trade Managers and confirmed locally for the contracts in their profiles. [Topstep's official hours](https://help.topstep.com/en/articles/8284206-when-and-what-products-can-i-trade) require earlier product closes to be respected, including separate CBOT agriculture windows and CME agriculture hours. The [published holiday schedule](https://help.topstep.com/en/articles/13350348-topstep-holiday-trading-hours) supplies the independent 2026 cutoff caps. Product-hour configuration examples are operator choices within those published limits, not discovered exchange calendars.

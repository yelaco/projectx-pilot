# Attention reports and operator-controlled positions

Automatic position closing is disabled. The application remembers the last verified exposure on each durable intent, with a UTC timestamp, and continues read-only reconciliation after restart. That record is historical evidence, never current flatness or execution authorization. Broker SL/TP and provider-side actions remain independent of the application.

Technical errors, protection discrepancies, legacy risk thresholds and scheduled exit reminders pause entries and generate local attention reports. They never cancel protection or close exposure. Only your authenticated, password-confirmed local emergency Flatten action can request closing. A pending entry can be cancelled automatically only while wholly unfilled with no current or previously verified exposure; partial remainders require operator review. The schedule's legacy `flatten` field is an exit reminder, not enforced closing. Legacy limits are not guaranteed daily-loss or floor protection.

## Optional Discord delivery

Open **Connection settings → Discord attention reports**. Supply a Discord incoming webhook URL, enable reports, and confirm with your administrator password. Leave the URL blank to retain it; disable reports to stop new deliveries, or remove the saved webhook. Notification settings are independent of connection/trading settings and can be changed while a position is open. Saving them never arms, pauses or submits trades.

The webhook is stored in `attention-discord.json` inside the private state directory with mode 0600, never rendered back into HTML or included in logs/status. Only HTTPS `discord.com` API webhook paths are accepted; proxies and redirects are disabled. The sender has no trading client or inbound command handler.

Execution attention/error reports include account ID, fixed diagnostic text and UTC time. No signal payload, order request, ProjectX credential, webhook credential or raw provider response is sent. Reports use Discord `allowed_mentions.parse: []` so content cannot ping users or roles. `wait=true` requests confirmation. Official references: [Execute Webhook](https://docs.discord.com/developers/resources/webhook#execute-webhook) and [rate limits](https://docs.discord.com/developers/topics/rate-limits), reviewed 2026-11-16.

Delivery runs in a separate bounded worker, with an eight-second HTTP timeout and a 64-report queue. Identical reports are suppressed for five minutes; tracking is bounded to 256 distinct messages. Queue/tracking overflow is counted in settings. Reports are best effort, not durable: offline delivery, suspension, process exit or queue overflow can lose them. Reports produced while disabled are discarded; saving settings invalidates queued reports from the previous configuration. An already in-flight request cannot be recalled.

The worker honors Discord rate/reset headers and `Retry-After`/`retry_after` on HTTP 429, with a conservative 60-second cooldown if no valid delay is available (bounded to 24 hours). It does not retry a report after errors or ambiguous delivery. HTTP 401/403/404 suspends delivery until settings are saved again. The local dashboard shows fixed delivery errors, last confirmed send and overflow counts without exposing response bodies or webhook URLs. Delivery failure never changes a trading position or triggers emergency management. Local monitoring is the authoritative report; Discord is optional convenience.

Only mocked HTTP transports and synthetic accounts were used in tests. No real Discord message or trading request was sent during development.

# Forex Factory US FOMC filter

Implemented default: `news_filter: us_fomc`. Configure on Operation days or
Settings → News filter. `off` explicitly disables it. Saving policy changes pauses
execution as usual; start locally after review. There is no embedded Fed schedule
and no Fed-calendar fallback.

Source: https://nfs.faireconomy.media/ff_calendar_thisweek.json

The native bot fetches the fixed HTTPS endpoint at startup and every six hours.
Failures retry after thirty minutes; manual refresh and startup requests share a
five-minute cooldown. No proxy or bridge was added to ProjectX execution. Fetches
use a named User-Agent and 15-second timeout, reject redirects, bound response size
to 1 MiB and validate the JSON/events before replacing the cache. Direct source
fields were inspected during implementation: title, country, date (explicit UTC
offset), impact, forecast and previous. A read-only Go-client live fetch succeeded.
The feed has no application-level availability guarantee; successful access now
does not guarantee future availability.

Only USD events matching these normalized titles block trading:

- FOMC Statement
- FOMC Press Conference
- FOMC Economic Projections
- Federal Funds Rate
- FOMC Rate Decision / FOMC Interest Rate Decision

FOMC member speeches, meeting minutes and the financial stability report are not
included. Unknown USD titles containing FOMC invalidate the refresh for review,
rather than silently accepting a possibly renamed policy event. A rejected refresh
retains existing cached data; if none is available, entries remain unrestricted
by news, with an error and coverage warning displayed. Other economic
releases/countries do not create news blocks. Impact alone does not classify FOMC.

Each matched event blocks its Chicago trading date (events after 17:00 belong to
the next trading date), including the prior evening
from 17:00. Only feed event dates are blocked: a two-day meeting's first day is NOT
inferred from a second-day announcement. Timestamps are parsed with their supplied
offset; source-week coverage uses Sunday–Saturday in America/New_York. This source
week differs from the admin's Saturday–Friday planning view. Date conversion and
session checks use America/Chicago and account for DST.

A validated source week with no matched policy events permits entry-day eligibility
for that week; an empty/invalid response does not. The feed must span one source
week and represent the current or immediately upcoming week at retrieval. Later
weeks remain unavailable until published, including when planning at the weekend.
Missing coverage does not restrict entries: the news filter has no rule to apply
for that date. A cache older than 72 hours produces a freshness warning, not a
blanket entry block. Known cached matching event blocks remain active until a
valid refresh updates them. A failed, malformed, oversized, 403 or 429 response
does not erase a cached block or manufacture restrictions for uncovered dates. Calendar omissions not detectable from the feed remain a limitation:
valid JSON cannot prove the publisher has included every real-world event.

Up to three weekly snapshots persist atomically in private `news-calendar.json`
with mode 0600. Restart loads validated snapshots but never restores arming or
replays a signal. News refresh does not arm execution. Operation days shows source,
last attempt/success, cached source weeks, per-day event blocks and informational coverage warnings,
last error and a protected Refresh news feed button. Refresh outcomes also appear
in Activity; raw feed bodies and events are not logged.

The gate applies before manual/automatic entry and again at submission. Blocked
alerts are consumed, never queued for later. Wholly unfilled entries with no current or previously verified exposure cancel when
the monitor observes a known news-event block. Missing/stale coverage alone does
not cancel pending entries. Filled exposure retains
broker Auto OCO and attention-only risk/schedule monitoring; a feed outage alone does not
flatten it. The fixed 30-minute limit lifetime and earlier cutoffs remain active.
News never overrides general or per-Trade Manager manual restrictions.

Unit tests use mock HTTP transports and cover schema/size/week validation, cached
blocks, title matching, timezone/session boundaries, no-event eligibility, refresh
errors, staleness, restart, concurrency, cancellation and entry/protection gates.
An external read-only feed check is opt-in and never touches a trading endpoint:

```sh
PROJECTX_NEWS_INTEGRATION=1 go test -v ./internal/news -run '^TestForexFactoryIntegration$' -count=1
```

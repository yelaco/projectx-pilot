# US FOMC entry filter

Implemented default: `news_filter: us_fomc`. Configure in Settings → ProjectX
connection → News filter, or YAML. `off` explicitly disables it. Saving Settings
pauses execution as usual; start locally after reviewing changes. Existing
configurations without this field default to US FOMC filtering.

Both days of every scheduled US Federal Reserve FOMC meeting are blocked. This
is a conservative application policy, not a Topstep trading prohibition. It
blocks the entire Chicago trading session for each meeting date, beginning at
17:00 the prior evening. For December 8–9, 2026, for example, entries are blocked
from December 7 at 17:00 through December 9 at 17:00 Chicago. Ordinary market-hours
rules still apply independently; the filter clearing never guarantees an open
market or permission to trade. UTC and laptop display time do not determine the
meeting session. America/Chicago handles DST.

The gate applies to both manual Practice tickets and automation, including a
fresh check at submission. New automatic alerts are consumed as blocked outcomes,
not queued to fire after the meeting. Continuous Start remains requested and its
status explains the news block; existing calendar/risk reconciliation still gates
future sessions. New entries need a fresh signal after the block.

Working entry remainders cancel if reconciliation observes a blocked/unknown news
session. Filled exposure is not closed because of the news filter; protection,
risk monitoring, emergency management and scheduled flattening continue. The
30-minute limit lifetime and earlier session cutoffs remain active. Active intents
persist their news-policy snapshot; Settings changes already pause execution and
cancel outstanding remainders. Restart does not arm or replay entries.

The lightweight local date table is reviewed for 2026 and 2027 as of November 14,
2026 (`session.NewsCalendarRevision`). Unknown years block entries with the filter
on. Calendar changes require a reviewed application update; no feed/scraper or
background external requests were added. Unscheduled meetings, newly revised dates,
CPI, NFP, speeches, minutes-release dates and other countries are not detected.
The 2027 dates are tentative and include the October strategy meeting; they do
not establish that the separate trading-session calendar supports 2027.

Official sources:

- [Federal Reserve meeting calendar](https://www.federalreserve.gov/monetarypolicy/fomccalendars.htm)
- [November 12, 2026 revised 2027 schedule](https://www.federalreserve.gov/newsevents/pressreleases/monetary20261112a.htm)

Tests cover prior-evening boundaries, DST/time zones, both meeting days, the
revised 2027 schedule, unknown years, explicit disabling, entry/submission gating,
pending cancellation, protected filled positions and authenticated settings
persistence. No live orders were sent during implementation.

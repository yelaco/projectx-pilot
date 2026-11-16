# Operation days

Implemented dashboard: **Operation days** (`/operation-days`). Each week displays
Saturday, Sunday, Monday, Tuesday, Wednesday, Thursday, Friday. Previous/next
navigation allows weekend planning of the coming week. Dates and boundaries use
America/Chicago, not the laptop's displayed timezone.

The hierarchy is restrictive:

1. General date blocks apply to every account, automation and Trade Manager.
2. The global US FOMC filter adds automatic blocks (enabled by default).
3. Each Trade Manager has a lower calendar to add its own blocked dates.

Clearing a manager checkbox cannot override a general/news block. Each card
shows inherited general/news reasons as well as that scope's manual choice.
This only restricts entry eligibility: enabled cards do not guarantee market
hours, account permission, fresh data or sufficient risk. Existing session/holiday
rules still apply. FOMC blocks come only from the automatically refreshed Forex
Factory weekly feed. Missing/stale coverage is informational and does not itself
block entries; known cached event blocks remain. Future weeks may not yet be published. See [news filter](news-filter.md).

Choices are specific dates, not recurring weekdays. Saving one week/scope
preserves other weeks and scopes. A new week has weekdays enabled unless blocked
by explicit dates or the news filter. No mandatory weekly approval was added.
Saturday/Sunday cards represent trading dates: Monday's session starts Sunday at
17:00 Chicago. Blocking Monday therefore blocks that Sunday evening too. A Sunday
card does not represent the Monday trading session. Trading calendar restrictions
continue to prevent weekend entries.

Saving requires authenticated same-origin CSRF-protected POST and pauses entries.
Review the policy and use the existing explicit Start again. The bot keeps
monitoring positions; pause or an operation-day block requests cancellation of
entry remainders, while filled exposure retains Auto OCO/risk management and
scheduled exit reminders. These restrictions also gate manual Practice entry tickets
and are rechecked at submission. Blocked alerts are consumed, never delayed until
an enabled day. The independent fixed 30-minute limit lifetime remains unchanged.

Policies persist in private YAML alongside other configuration:

```yaml
news_filter: us_fomc
no_trade_dates:
  - '2026-12-07'
trade_manager_no_trade_dates:
  practice-conservative:
    - '2026-12-10'
```

On restart the manager restores the plan before execution workers start. Recovery
does not restore arming. Dates inherited by an intent use its pinned Trade Manager
ID, not an alert-supplied account or manager. Multi-account policies are still
configuration only where execution is unsupported; this page does not enable them.
Keep plan IDs aligned when deleting/recreating a Trade Manager: a reused ID retains
that ID's stored date restrictions. Removing a manager does not erase history.

Tests cover Saturday-Friday ordering, Chicago/Sunday session boundaries,
inherited versus manager-specific blocks, preservation of other weeks, YAML
persistence, authenticated/CSRF-protected writes, unknown scopes, invalid dates,
entry/submission enforcement, pending cancellation and retained filled protection.
No live orders were submitted. Visual browser inspection has not been performed.

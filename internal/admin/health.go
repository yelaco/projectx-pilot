package admin

import (
	"fmt"
	"sort"
	"time"

	"github.com/yelaco/projectx-pilot/internal/attention"
	"github.com/yelaco/projectx-pilot/internal/execution"
	"github.com/yelaco/projectx-pilot/internal/lockreview"
	"github.com/yelaco/projectx-pilot/internal/news"
	"github.com/yelaco/projectx-pilot/internal/session"
)

// HealthCheck is a diagnostic observation, never an execution permission.
type HealthCheck struct {
	ID, Group, Name, State, Label, Symbol, Summary, Detail, Action, Href string
	AccountID                                                            int64
	ObservedAt                                                           time.Time
}
type SystemHealth struct {
	CapturedAt                      time.Time
	ConnectionConnected             bool
	Attention, Blocked, Unknown, OK int
	Checks                          []HealthCheck
}
type healthMeta struct {
	LeaseHeld                     bool
	Manual                        execution.Status
	ManualLock                    string
	ManualLockKnown               bool
	Refresh, Timeout              time.Duration
	News                          news.Status
	NewsMode                      string
	SystemReports, TradingReports attention.Status
}

func (m *Manager) healthMeta() healthMeta {
	m.mu.Lock()
	meta := healthMeta{LeaseHeld: m.executionLease != nil, Refresh: m.c.RefreshInterval, Timeout: m.c.RequestTimeout, NewsMode: m.c.NewsFilter}
	m.mu.Unlock()
	meta.Manual = m.executor.Status()
	id := meta.Manual.AccountID
	for _, i := range meta.Manual.Intents {
		if !healthTerminal(i.State) {
			id = i.AccountID
			break
		}
	}
	if id > 0 {
		var err error
		meta.ManualLock, err = m.executionStore.ExecutionLock(fmt.Sprint(id), session.CurrentTradingDate(m.now()))
		meta.ManualLockKnown = err == nil
	}
	meta.News = m.NewsStatus()
	meta.SystemReports = m.AttentionStatus()
	meta.TradingReports = m.TradingReportStatus()
	return meta
}
func healthTerminal(state string) bool {
	return state == "closed" || state == "rejected" || state == "cancelled"
}
func healthLabel(state string) (string, string) {
	switch state {
	case "ok":
		return "OK", "✓"
	case "attention":
		return "Needs attention", "!"
	case "blocked":
		return "Blocked", "!"
	case "unknown":
		return "Not verified", "?"
	case "off":
		return "Off", "—"
	case "history":
		return "History", "•"
	default:
		return "Waiting / idle", "•"
	}
}
func healthFresh(at, now time.Time, maxAge time.Duration) bool {
	return !at.IsZero() && !at.After(now.Add(time.Second)) && now.Sub(at) <= maxAge
}

// buildSystemHealth uses only already-collected snapshots. It performs no I/O.
func buildSystemHealth(v Monitor, meta healthMeta, now time.Time) SystemHealth {
	h := SystemHealth{CapturedAt: now.UTC(), ConnectionConnected: v.State.Connected, Checks: []HealthCheck{}}
	add := func(check HealthCheck) {
		check.Label, check.Symbol = healthLabel(check.State)
		switch check.State {
		case "attention":
			h.Attention++
		case "blocked":
			h.Blocked++
		case "unknown":
			h.Unknown++
		case "ok":
			h.OK++
		}
		h.Checks = append(h.Checks, check)
	}
	add(HealthCheck{ID: "lease", Group: "Local service", Name: "Execution process ownership", State: choose(meta.LeaseHeld, "ok", "attention"), Summary: choose(meta.LeaseHeld, "This process holds the execution lease.", "Execution ownership has not been established in this process."), Detail: "A running dashboard alone does not establish execution readiness.", Action: "Inspect account runners", Href: "/runners"})
	add(HealthCheck{ID: "manual-monitor", Group: "Local service", Name: "Execution monitor", State: choose(meta.Manual.MonitorRunning, "ok", "attention"), Summary: choose(meta.Manual.MonitorRunning, "The execution monitor is running.", "The execution monitor is not running."), Action: "Inspect account runners", Href: "/runners"})
	add(HealthCheck{ID: "storage", Group: "Local service", Name: "Recent monitoring storage reads", State: choose(v.Error == "", "ok", "attention"), Summary: choose(v.Error == "", "Existing monitoring reads completed.", "Some monitoring storage could not be read."), Detail: "This is not a disk-space or write-durability test.", ObservedAt: now.UTC(), Action: "Inspect Activity", Href: "/activity"})
	refresh := meta.Refresh*2 + meta.Timeout
	if refresh < time.Minute {
		refresh = time.Minute
	}
	state, summary := "unknown", "No successful discovery snapshot has been observed."
	if v.State.Connected && healthFresh(v.State.UpdatedAt, now, refresh) {
		state = "ok"
		summary = "Account discovery has a recent successful snapshot."
	} else if !v.State.UpdatedAt.IsZero() {
		state = "attention"
		summary = "Discovery is disconnected or its last successful snapshot is old."
	} else if v.State.Error != "" {
		state = "attention"
		summary = "Account discovery is unavailable or not configured."
	}
	add(HealthCheck{ID: "discovery", Group: "Connections", Name: "ProjectX account discovery", State: state, Summary: summary, Detail: "Discovery health is separate from fresh entry checks and live position protection.", ObservedAt: v.State.UpdatedAt, Action: "Inspect connection settings", Href: "/settings"})
	state, summary = "idle", "No account event subscriptions are needed yet."
	if v.UserData.Accounts > 0 {
		state = choose(v.UserData.Connected, "ok", "attention")
		summary = choose(v.UserData.Connected, "Account event stream connected.", "Account event stream disconnected; REST reconciliation remains the fallback.")
	}
	add(HealthCheck{ID: "user-stream", Group: "Connections", Name: "Account event stream", State: state, Summary: summary, Detail: fmt.Sprintf("%d subscribed accounts. A quiet stream alone is not a failure.", v.UserData.Accounts), ObservedAt: v.UserData.LastEvent, Action: "Inspect account runners", Href: "/runners"})
	state, summary = "off", "Webhook receiving is disabled."
	if v.ReceivingEnabled {
		state = choose(v.ReceiverListening, "ok", "attention")
		summary = choose(v.ReceiverListening, "The local receiver answered its existing loopback health check.", "The local receiver did not answer successfully.")
	}
	add(HealthCheck{ID: "receiver", Group: "Connections", Name: "Local alert receiver", State: state, Summary: summary, Detail: "This does not prove TradingView or public delivery reachability.", Action: "Inspect webhook settings", Href: "/settings/webhooks"})
	state, summary = "off", "Public alert ingress is disabled."
	if v.ReceivingEnabled {
		switch v.PublicURLKind {
		case "Temporary Quick Tunnel":
			state = choose(v.Tunnel.State == "running" && v.PublicURL != "", "ok", "attention")
			summary = choose(state == "ok", "The managed ingress process reports running.", "The managed ingress process is not ready.")
		case "Permanent hostname":
			state = "unknown"
			summary = "A permanent hostname is configured; external reachability is not probed."
		default:
			state = "unknown"
			summary = "No public ingress address is configured; local-only receiving may be intentional."
		}
	}
	add(HealthCheck{ID: "public-ingress", Group: "Connections", Name: "Public alert ingress", State: state, Summary: summary, Detail: "The checklist never probes a public endpoint or exposes the local admin dashboard. An ingress process is not proof of end-to-end TradingView delivery.", Action: "Inspect webhook settings", Href: "/settings/webhooks"})
	add(HealthCheck{ID: "entry-gate", Group: "Entry controls", Name: "Global entry gate", State: choose(v.GlobalPaused, "blocked", "ok"), Summary: choose(v.GlobalPaused, "Entries are paused globally by the operator.", "The global entry gate is open; other checks still apply."), Action: "Inspect global controls", Href: "/runners"})
	if len(v.Runners) == 0 {
		add(HealthCheck{ID: "runner-scope", Group: "Entry controls", Name: "Account runners", State: "idle", Summary: "No automatic account runner has been created yet.", Action: "Inspect automations", Href: "/automations"})
	}
	activeAccounts := 0
	for _, runner := range v.Runners {
		for _, intent := range runner.Status.Intents {
			if !healthTerminal(intent.State) {
				activeAccounts++
				break
			}
		}
	}
	backstop := time.Duration(activeAccounts) * 3 * time.Second
	if backstop < 5*time.Second {
		backstop = 5 * time.Second
	}
	maxAge := 12*time.Second + 2*backstop
	for _, r := range v.Runners {
		appendAccountHealth(add, r.AccountID, r.Status, r.LockReason, r.LockReason != "Local lock status unavailable", now, maxAge, "/runners")
	}
	manualID := meta.Manual.AccountID
	hasManual := meta.Manual.Enabled
	for _, i := range meta.Manual.Intents {
		if !healthTerminal(i.State) {
			manualID = i.AccountID
			hasManual = true
			break
		}
	}
	if hasManual && manualID > 0 {
		appendAccountHealth(add, manualID, meta.Manual, meta.ManualLock, meta.ManualLockKnown, now, maxAge, "/execution")
	}
	addDeliveryHealth(add, "system-discord", "System attention reports", meta.SystemReports)
	addDeliveryHealth(add, "trading-discord", "Daily trading reports", meta.TradingReports)
	state, summary = "off", "News filtering is off."
	if meta.NewsMode != "off" {
		state = "unknown"
		summary = "No recent news calendar snapshot has been observed."
		if meta.News.LastError != "" {
			state = "attention"
			summary = "The latest news refresh failed; cached restrictions may still apply."
		} else if healthFresh(meta.News.LastSuccess, now, news.MaxAge) {
			state = "ok"
			summary = "The cached news calendar is recent."
		} else if !meta.News.LastSuccess.IsZero() || meta.News.LastError != "" {
			state = "attention"
			summary = "The news calendar is stale or its refresh failed."
		}
	}
	add(HealthCheck{ID: "news", Group: "Supporting data", Name: "News calendar", State: state, Summary: summary, Detail: "Missing/stale coverage alone does not block entries; known cached event blocks still apply. Session and holiday authorization is checked separately before entry.", ObservedAt: meta.News.LastSuccess, Action: "Inspect operation days", Href: "/operation-days"})
	rank := map[string]int{"attention": 0, "blocked": 1, "unknown": 2, "ok": 3, "idle": 4, "off": 5, "history": 6}
	sort.SliceStable(h.Checks, func(a, b int) bool { return rank[h.Checks[a].State] < rank[h.Checks[b].State] })
	return h
}
func choose(condition bool, yes, no string) string {
	if condition {
		return yes
	}
	return no
}
func appendAccountHealth(add func(HealthCheck), account int64, s execution.Status, reason string, lockKnown bool, now time.Time, maxAge time.Duration, href string) {
	group := fmt.Sprintf("Account #%d", account)
	row := func(id, name, state, summary, detail string, at time.Time) {
		add(HealthCheck{ID: fmt.Sprintf("account-%d-%s", account, id), AccountID: account, Group: group, Name: name, State: state, Summary: summary, Detail: detail, ObservedAt: at, Action: "Inspect account", Href: href})
	}
	row("monitor", "Account monitor", choose(s.MonitorRunning, "ok", "attention"), choose(s.MonitorRunning, "The account monitor is running.", "The account monitor is not running."), "", time.Time{})
	state, summary := "ok", "No durable lock is recorded for the current Chicago session."
	detail := ""
	if !lockKnown {
		state = "unknown"
		summary = "Local lock status could not be read."
	} else if reason != "" {
		state = "blocked"
		policy := lockreview.Classify(reason)
		summary = policy.Category + " blocks new entries."
		detail = policy.Guidance
		if policy.Category != "Unrecognized lock" {
			detail = reason + ". " + detail
		}
	}
	row("lock", "Local session lock", state, summary, detail, time.Time{})
	var active *execution.Intent
	for n := range s.Intents {
		if s.Intents[n].AccountID == account && !healthTerminal(s.Intents[n].State) {
			active = &s.Intents[n]
			break
		}
	}
	state, summary = "idle", "The operator has not started this account."
	if s.Armed {
		state = "ok"
		summary = "Armed for new alerts; each order still requires fresh entry checks."
	} else if s.RecoveryPending {
		state = "attention"
		summary = "The original Start is retained; waiting for fault recovery and fresh flat/account/market checks."
	} else if s.RunRequested {
		summary = "Start is requested; this account is waiting for readiness."
	}
	if active != nil {
		state = "blocked"
		summary = "An existing execution occupies this account; new entries are blocked."
	}
	row("entries", "Entry readiness", state, summary, "New entries require fresh arming and risk checks. Automatic recovery may resume a retained Start; no old alert is replayed.", s.ArmedAt)
	quoteState, quoteSummary := "idle", "No active quote subscription is required at this point."
	if s.Quotes.Contract != "" {
		if s.Quotes.Fresh && healthFresh(s.Quotes.At, now, 5*time.Second) {
			quoteState = "ok"
			quoteSummary = "The existing market-data cache contains a fresh quote."
		} else if s.Armed || s.RecoveryPending || active != nil && !active.Policy.PerTradeOnly {
			quoteState = "attention"
			quoteSummary = "A required fresh quote is unavailable."
		} else {
			quoteSummary = "A fresh quote is unavailable; current per-trade-only management does not require it."
		}
	}
	row("quotes", "Market quote freshness", quoteState, quoteSummary, "No extra subscription or broker request is made by this checklist. Quote status does not establish SL/TP coverage.", s.Quotes.At)
	if active == nil {
		return
	}
	recent := healthFresh(active.ObservedAt, now, maxAge)
	state, summary = "unknown", "No complete reconciliation cycle has been observed in this process."
	if s.ReconcileError != "" {
		state = "attention"
		summary = "The latest reconciliation cycle failed."
	} else if healthFresh(s.LastReconcileSuccess, now, maxAge) {
		state = "ok"
		summary = "The account reconciliation cycle completed recently."
	} else if !s.LastReconcileAttempt.IsZero() {
		state = "attention"
		summary = "Reconciliation has not completed recently."
	}
	row("reconciliation", "Reconciliation cycle", state, summary, "Only existing background checks are observed here; this page does not start another check.", s.LastReconcileSuccess)
	state, summary = "unknown", "No recent complete account observations are available."
	if s.ReconcileError != "" {
		state = "attention"
		summary = "The latest reconciliation did not complete; the last observations are historical."
	} else if recent {
		state = "ok"
		summary = fmt.Sprintf("Last observed: %d position(s), %d working order(s).", len(active.ObservedPositions), len(active.ObservedOrders))
	} else if !active.ObservedAt.IsZero() {
		state = "attention"
		summary = "The last complete exposure observations are old."
	}
	row("observations", "Exposure observations", state, summary, "An old verified position record is retained for recovery; it is not a current broker observation.", active.ObservedAt)
	state, summary = "unknown", "Protection has not been verified by the latest reconciliation."
	if s.ReconcileError == "" && recent && s.ProtectionCheck == "verified" && healthFresh(s.ProtectionCheckedAt, now, maxAge) {
		state = "ok"
		summary = "The latest reconciliation observed matching linked SL/TP coverage."
	} else if s.ProtectionCheck == "mismatch" {
		state = "attention"
		summary = "The latest protection check found missing or inconsistent coverage."
	} else if s.ReconcileError == "" && recent && len(active.ObservedPositions) == 0 {
		state = "idle"
		summary = "No position was present in the last account observation; terminal reconciliation may still be pending."
	}
	row("protection", "Observed SL/TP coverage", state, summary, "No automatic closing or protective-order changes are triggered. Acknowledgement is not proof of protection.", s.ProtectionCheckedAt)
	if active.AttentionRequired {
		summary = "The current execution needs attention."
		if active.AcknowledgedLockActive {
			summary = "The fault was acknowledged, but its condition is still present."
		}
		row("attention", "Execution attention", "attention", summary, "Inspect the current account state. The first management trigger is retained separately as history.", active.Updated)
	}
	if active.EmergencyAttempted {
		row("emergency", "Emergency action result", "attention", "An emergency attempt is durably recorded; verify the result.", "An ambiguous attempt is never replayed automatically.", active.Updated)
	}
	if !active.Window.LastEntry.IsZero() {
		state = "idle"
		summary = "The captured entry window has not reached its cutoff."
		if !now.Before(active.Window.LastEntry) {
			state = "blocked"
			summary = "The captured last-entry time has passed."
		}
		row("schedule", "Captured schedule", state, summary, "Entry cutoff: "+active.Window.LastEntry.UTC().Format(time.RFC3339)+". Exit reminder: "+active.Window.Flatten.UTC().Format(time.RFC3339)+". The reminder does not close exposure.", time.Time{})
	}
	risk := choose(active.Policy.PerTradeOnly, "Per-trade-only policy: no account-floor, MLL or cumulative daily-loss protection is calculated or enforced.", "Legacy account-risk configuration: entry checks still require fresh data; automatic risk exits are disabled.")
	row("policy", "Captured risk policy", "history", risk, "Stored policy is configuration, not evidence that an account limit is enforced.", time.Time{})
	if active.HaltReason != "" {
		policy := lockreview.Classify(active.HaltReason)
		history := "A previous management trigger is retained; inspect the account for its details."
		if policy.Category != "Unrecognized lock" {
			history = active.HaltReason
		}
		row("trigger-history", "Original trigger (history)", "history", history, "This historical trigger alone does not mean the underlying issue is still active.", time.Time{})
	}
}
func addDeliveryHealth(add func(HealthCheck), id, name string, status attention.Status) {
	state, summary := "off", "This optional Discord channel is disabled."
	if status.Enabled {
		switch {
		case !status.Configured:
			state = "attention"
			summary = "Delivery is enabled but no webhook is configured."
		case status.LastError != "":
			state = "attention"
			summary = "The last delivery attempt or queue operation reported an error; current reachability is not tested."
		case status.LastSent.IsZero():
			state = "unknown"
			summary = "Configured, but no confirmed delivery has been observed yet."
		default:
			state = "ok"
			summary = "The most recent delivery was confirmed; current reachability is not tested."
		}
	}
	add(HealthCheck{ID: id, Group: "Discord reports", Name: name, State: state, Summary: summary, Detail: fmt.Sprintf("%d report(s) could not be queued earlier in this process (historical count). System and trading webhooks are independent; no test message is sent here.", status.Dropped), ObservedAt: status.LastSent, Action: "Inspect report settings", Href: "/settings"})
}

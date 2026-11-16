package admin

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yelaco/projectx-pilot/internal/app"
	"github.com/yelaco/projectx-pilot/internal/attention"
	"github.com/yelaco/projectx-pilot/internal/execution"
	"github.com/yelaco/projectx-pilot/internal/fleet"
	"github.com/yelaco/projectx-pilot/internal/projectx"
	"github.com/yelaco/projectx-pilot/internal/realtime"
	"github.com/yelaco/projectx-pilot/internal/risk"
	"github.com/yelaco/projectx-pilot/internal/storage"
)

func healthRow(t *testing.T, h SystemHealth, id string) HealthCheck {
	t.Helper()
	for _, row := range h.Checks {
		if row.ID == id {
			return row
		}
	}
	t.Fatal("missing checklist row", id)
	return HealthCheck{}
}
func TestHealthShowsRecoveredObservationsSeparatelyFromRetainedLock(t *testing.T) {
	now := time.Now().UTC()
	intent := execution.Intent{ID: "held", AccountID: 42, State: "protected", ObservedAt: now, ObservedPositions: []projectx.Position{{ID: 1, Size: 1}}, HaltReason: "market data stale while managing exposure", Policy: risk.Profile{PerTradeOnly: true}}
	status := execution.Status{MonitorRunning: true, LastReconcileAttempt: now, LastReconcileSuccess: now, ProtectionCheck: "verified", ProtectionCheckedAt: now, Quotes: realtime.QuoteStatus{Contract: "mnq", Fresh: true, At: now}, Intents: []execution.Intent{intent}}
	monitor := Monitor{State: app.Snapshot{Connected: true, UpdatedAt: now}, Runners: []fleet.View{{AccountID: 42, Status: status, LockReason: "market data stale while managing exposure"}}}
	meta := healthMeta{LeaseHeld: true, Manual: execution.Status{MonitorRunning: true}, NewsMode: "off"}
	h := buildSystemHealth(monitor, meta, now)
	if healthRow(t, h, "account-42-protection").State != "ok" || healthRow(t, h, "account-42-lock").State != "blocked" || healthRow(t, h, "account-42-trigger-history").State != "history" {
		t.Fatal("current recovery, lock and history were conflated")
	}
	// Acknowledgement never makes missing protection healthy.
	monitor.Runners[0].LockReason = ""
	monitor.Runners[0].Status.ProtectionCheck = "mismatch"
	monitor.Runners[0].Status.Intents[0].AttentionRequired = true
	monitor.Runners[0].Status.Intents[0].AcknowledgedLockActive = true
	h = buildSystemHealth(monitor, meta, now)
	if healthRow(t, h, "account-42-protection").State != "attention" || !strings.Contains(healthRow(t, h, "account-42-attention").Summary, "still present") {
		t.Fatal("acknowledgement treated as recovery")
	}
}
func TestHealthNeverTreatsHistoricalOrFailedChecksAsCurrentProtection(t *testing.T) {
	now := time.Now().UTC()
	status := execution.Status{MonitorRunning: true, ProtectionCheck: "verified", ProtectionCheckedAt: now.Add(-time.Hour), Intents: []execution.Intent{{ID: "recovered", AccountID: 42, State: "protected", ObservedAt: now.Add(-time.Hour), VerifiedAt: now.Add(-time.Minute), VerifiedPosition: &projectx.Position{ID: 1, Size: 1}}}}
	v := Monitor{Runners: []fleet.View{{AccountID: 42, Status: status}}}
	h := buildSystemHealth(v, healthMeta{NewsMode: "off"}, now)
	if healthRow(t, h, "account-42-protection").State == "ok" {
		t.Fatal("historical verified position promoted to current protection")
	}
	v.Runners[0].Status.Intents[0].ObservedAt = now
	v.Runners[0].Status.ProtectionCheckedAt = now
	v.Runners[0].Status.ReconcileError = "positions unavailable; entries paused"
	h = buildSystemHealth(v, healthMeta{NewsMode: "off"}, now)
	if healthRow(t, h, "account-42-protection").State == "ok" || healthRow(t, h, "account-42-observations").State != "attention" {
		t.Fatal("failed latest check left a healthy protection indication")
	}
}
func TestHealthQuoteAgeAndIdleAccountsDoNotHideStaleExposure(t *testing.T) {
	now := time.Now().UTC()
	v := Monitor{Runners: []fleet.View{{AccountID: 42, Status: execution.Status{Armed: true, Quotes: realtime.QuoteStatus{Contract: "mnq", Fresh: true, At: now.Add(-time.Minute)}, Intents: []execution.Intent{{ID: "held", AccountID: 42, State: "protected", ObservedAt: now.Add(-time.Minute)}}}}}}
	for n := 0; n < 50; n++ {
		v.Runners = append(v.Runners, fleet.View{AccountID: int64(100 + n)})
	}
	h := buildSystemHealth(v, healthMeta{NewsMode: "off"}, now)
	if healthRow(t, h, "account-42-quotes").State != "attention" || healthRow(t, h, "account-42-observations").State != "attention" {
		t.Fatal("cached fresh flag or idle accounts hid old observations")
	}
}
func TestHealthOptionalReportsDoNotClaimUntestedDeliveryAndExportIsNarrow(t *testing.T) {
	v := Monitor{WebhookURLs: map[string]string{"private": "WEBHOOK_SECRET"}, PublicURL: "DELIVERY_TOKEN", Activity: []storage.Activity{{Message: "RAW_PAYLOAD"}}}
	h := buildSystemHealth(v, healthMeta{NewsMode: "off", SystemReports: attention.Status{}, TradingReports: attention.Status{Enabled: true, Configured: true}}, time.Now())
	if healthRow(t, h, "system-discord").State != "off" || healthRow(t, h, "trading-discord").State != "unknown" {
		t.Fatal("disabled/unverified reports mislabeled")
	}
	body, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"WEBHOOK_SECRET", "DELIVERY_TOKEN", "RAW_PAYLOAD"} {
		if strings.Contains(string(body), secret) {
			t.Fatal("health export exposed unrelated private monitor data")
		}
	}
}

func TestHealthRecoveredDiscordDeliveryDoesNotKeepHistoricalDropsActive(t *testing.T) {
	now := time.Now().UTC()
	h := buildSystemHealth(Monitor{}, healthMeta{NewsMode: "off", SystemReports: attention.Status{Enabled: true, Configured: true, LastSent: now, Dropped: 2}}, now)
	if healthRow(t, h, "system-discord").State != "ok" || !strings.Contains(healthRow(t, h, "system-discord").Detail, "historical count") {
		t.Fatal("historical drop count mislabeled as current delivery failure")
	}
}

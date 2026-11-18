package admin

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yelaco/projectx-pilot/internal/execution"
	"github.com/yelaco/projectx-pilot/internal/fleet"
	"github.com/yelaco/projectx-pilot/internal/risk"
)

func TestHourlyStatusUsesCurrentTelemetryAndNeverFreeFormSecrets(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	profiles := []Automation{{ID: "running", Name: "DO-NOT-SEND-NAME", ExecutionMode: "automatic", TradeManagerID: "a"}, {ID: "recovering", ExecutionMode: "automatic", TradeManagerID: "b"}, {ID: "paused", ExecutionMode: "automatic", AccountID: 3}, {ID: "review", ExecutionMode: "review", AccountID: 4}}
	managers := []risk.Profile{{ID: "a", AccountIDs: []int64{1}}, {ID: "b", AccountIDs: []int64{2}}}
	views := []fleet.View{{AccountID: 1, ProfileID: "running", Status: execution.Status{MonitorRunning: true, Armed: true, ArmedUntil: now.Add(time.Hour), RunRequested: true, Error: "DO-NOT-SEND-ERROR", RunMessage: "DO-NOT-SEND-MESSAGE"}}, {AccountID: 2, ProfileID: "recovering", LockReason: "technical fault", Status: execution.Status{MonitorRunning: true, RecoveryPending: true}}}
	message := strings.Join(automationStatusMessages(profiles, managers, views, false, now, "https://pilot.example.test"), "\n")
	for _, expected := range []string{"`running` — RUNNING", "`recovering` — WAITING FOR RECOVERY", "`paused` — PAUSED", "`review` — REVIEW ONLY", "locks 1", "https://pilot.example.test/automations"} {
		if !strings.Contains(message, expected) {
			t.Fatal("missing status", expected, message)
		}
	}
	if strings.Contains(message, "DO-NOT-SEND") {
		t.Fatal("free-form metadata leaked")
	}
	views[0].Status.ArmedUntil = now.Add(-time.Second)
	message = strings.Join(automationStatusMessages(profiles[:1], managers, views, false, now, "http://127.0.0.1:8080"), "\n")
	if strings.Contains(message, "— RUNNING") {
		t.Fatal("expired arming reported as running")
	}
}
func TestHourlyStatusRetainsRecoveredExposureAfterConfigurationChanges(t *testing.T) {
	now := time.Now().UTC()
	profiles := []Automation{{ID: "held", ExecutionMode: "automatic", TradeManagerID: "new-targets"}}
	managers := []risk.Profile{{ID: "new-targets", AccountIDs: []int64{99}}}
	views := []fleet.View{{AccountID: 42, Status: execution.Status{MonitorRunning: true, Intents: []execution.Intent{{AccountID: 42, ProfileID: "held", State: "protected"}}}}}
	message := strings.Join(automationStatusMessages(profiles, managers, views, false, now, "http://127.0.0.1:8080"), "\n")
	if !strings.Contains(message, "PAUSED / MONITORING EXECUTION") || !strings.Contains(message, "managing 1") {
		t.Fatal("recovered or captured exposure was hidden", message)
	}
}
func TestHourlyStatusSplitsWithoutOmittingAnyProfile(t *testing.T) {
	profiles := []Automation{}
	for n := 0; n < 100; n++ {
		profiles = append(profiles, Automation{ID: fmt.Sprintf("profile-%03d-%s", n, strings.Repeat("x", 45)), ExecutionMode: "automatic"})
	}
	messages := automationStatusMessages(profiles, nil, nil, false, time.Now(), "https://pilot.example.test")
	if len(messages) < 2 || len(messages) > 32 {
		t.Fatal("unexpected report chunk count", len(messages))
	}
	all := strings.Join(messages, "\n")
	for _, profile := range profiles {
		if strings.Count(all, "`"+profile.ID+"`") != 1 {
			t.Fatal("profile lost or duplicated", profile.ID)
		}
	}
	for _, message := range messages {
		if len([]rune(message)) > 1600 {
			t.Fatal("report exceeds sender budget")
		}
	}
}
func TestHourlyReportingIsOptInDurablyClaimedAndDoesNotArm(t *testing.T) {
	m, _, _, _ := setup(t)
	now := time.Date(2026, 11, 18, 12, 30, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	hour := now.Truncate(time.Hour)
	if err := m.notifications.Configure(true, "https://discord.com/api/webhooks/123/offline-test-token", false); err != nil {
		t.Fatal(err)
	}
	if err := m.processHourlyAutomationReport(); err != nil {
		t.Fatal(err)
	}
	if exists, err := m.executionStore.HourlyAutomationReportExists(hour); err != nil || exists {
		t.Fatal("disabled hourly report was claimed", exists, err)
	}
	if err := m.notifications.Configure(true, "", false, true); err != nil {
		t.Fatal(err)
	}
	if err := m.processHourlyAutomationReport(); err != nil {
		t.Fatal(err)
	}
	if exists, err := m.executionStore.HourlyAutomationReportExists(hour); err != nil || !exists {
		t.Fatal("hour not durably claimed", exists, err)
	}
	if err := m.processHourlyAutomationReport(); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Hour)
	if err := m.processHourlyAutomationReport(); err != nil {
		t.Fatal(err)
	}
	if exists, err := m.executionStore.HourlyAutomationReportExists(hour.Add(time.Hour)); err != nil || exists {
		t.Fatal("missed-hour backlog was generated")
	}
	if m.executor.Status().Armed || m.executor.Status().RunRequested {
		t.Fatal("report changed trading authority")
	}
	views, _ := m.fleet.Snapshot()
	if len(views) != 0 {
		t.Fatal("report created broker account runners")
	}
}

func TestHourlyStatusIncludesRetainedUnconfiguredExecutionsWithoutUnsafeLabels(t *testing.T) {
	views := []fleet.View{{AccountID: 42, Status: execution.Status{MonitorRunning: true, Intents: []execution.Intent{{AccountID: 42, ProfileID: "removed-profile", State: "protected"}, {AccountID: 42, ProfileID: "https://secret.invalid/token", State: "uncertain"}}}}}
	text := strings.Join(automationStatusMessages(nil, nil, views, false, time.Now(), "http://127.0.0.1:8080"), "\n")
	if !strings.Contains(text, "`removed-profile` — RETAINED / MONITORING EXECUTION") || strings.Contains(text, "secret.invalid") || !strings.Contains(text, "unrecognized-profile") {
		t.Fatal("retained execution hidden or unsafe identity leaked", text)
	}
}

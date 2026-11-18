package dashboard

import (
	"bytes"
	"github.com/yelaco/projectx-pilot/internal/execution"
	"github.com/yelaco/projectx-pilot/internal/fleet"
	"github.com/yelaco/projectx-pilot/internal/lockreview"
	"github.com/yelaco/projectx-pilot/internal/signal"
	"github.com/yelaco/projectx-pilot/internal/storage"
	"strings"
	"testing"
)

func TestRunnerLockControlsFollowCurrentLockAndSession(t *testing.T) {
	for _, reason := range []string{"market data stale while managing exposure", "protective orders missing, pending or inconsistent"} {
		for _, state := range []string{"current", "cleared", "other-session", "other-lock"} {
			t.Run(reason+state, func(t *testing.T) {
				lock, session := reason, "today"
				switch state {
				case "cleared":
					lock = ""
				case "other-session":
					session = "tomorrow"
				case "other-lock":
					lock = "daily loss limit reached"
				}
				v := view{Runners: []fleet.View{{AccountID: 42, LockReason: lock, LockSession: session, LockPolicy: lockreview.Classify(lock), Status: execution.Status{Intents: []execution.Intent{{ID: "original", AccountID: 42, Session: "today", State: "closed", HaltReason: reason}}}}}}
				var buf bytes.Buffer
				if err := page.ExecuteTemplate(&buf, "runners", v); err != nil {
					t.Fatal(err)
				}
				action := "/fleet/review-stale-quote"
				if strings.Contains(reason, "protective") {
					action = "/fleet/review-oco"
				}
				if got := strings.Contains(buf.String(), action); got != (state == "current") {
					t.Fatalf("wrong review visibility for %s", state)
				}
				if !strings.Contains(buf.String(), "Original management triggers (history)") || !strings.Contains(buf.String(), reason) {
					t.Fatal("historical evidence lost")
				}
			})
		}
	}
}

func TestUnassignedDistributionDeliveryDoesNotRenderAccountZero(t *testing.T) {
	v := view{Deliveries: []storage.Record{{Signal: signal.Signal{ID: "unassigned"}}}, DeliveryJobs: map[string][]storage.TargetJob{"unassigned": {{AccountID: 0, State: "blocked", Message: "No ready account"}}}}
	var buf bytes.Buffer
	if err := page.ExecuteTemplate(&buf, "deliveries", v); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "<td>#0</td>") || !strings.Contains(buf.String(), "No account selected") {
		t.Fatal("unassigned distribution displayed as a real account")
	}
}

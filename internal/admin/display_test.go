package admin

import (
	"github.com/yelaco/projectx-pilot/internal/execution"
	"github.com/yelaco/projectx-pilot/internal/fleet"
	"testing"
	"time"
)

func TestProfileDisplayIndependentOfAccountOrderAndHistoryOwner(t *testing.T) {
	views := []fleet.View{
		{ProfileID: "current", Status: execution.Status{RunRequested: true, Armed: true, Intents: []execution.Intent{{ID: "old", ProfileID: "previous", Updated: time.Now()}}}},
		{ProfileID: "current", Status: execution.Status{RunRequested: true}},
	}
	a := profileStatuses(views)
	views[0], views[1] = views[1], views[0]
	b := profileStatuses(views)
	if a["current"].RunMessage != b["current"].RunMessage || a["current"].RunMessage != "Running · 1 of 2 accounts armed; inspect each account’s readiness" {
		t.Fatalf("order-dependent status: %+v %+v", a, b)
	}
	if len(a["current"].Intents) != 0 || len(a["previous"].Intents) != 1 || a["previous"].Armed || a["previous"].RunRequested {
		t.Fatalf("history leaked ownership: %+v", a)
	}
}
func TestExecutionModeIncludesAutomaticAccountArming(t *testing.T) {
	mode := displayExecutionMode(execution.Status{}, []fleet.View{{Status: execution.Status{Armed: true, Automated: true}}}, true)
	if mode != "Automatic execution armed" {
		t.Fatal(mode)
	}
}

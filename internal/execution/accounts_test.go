package execution

import (
	"context"
	"testing"
)

func TestAutomaticAccountEligibility(t *testing.T) {
	for _, kind := range []string{"practice", "combine", "express", "renamed", "live", "unknown", "disabled", "wrong-id"} {
		t.Run(kind, func(t *testing.T) {
			e, api, _, _, c, p, a, s, _ := fixture(t)
			c.Automated, c.AutomationProfileID, c.AutomationContractID = true, "swift", "mnq"
			a.AutomationConfirmed = true
			switch kind {
			case "combine":
				api.accounts[0].Name = "50KTC-evaluation"
			case "express":
				api.accounts[0].Name = "50KXFA-funded"
			case "renamed":
				api.accounts[0].Name = "My selected account"
			case "live":
				value := false
				api.accounts[0].Simulated = &value
			case "unknown":
				api.accounts[0].Simulated = nil
			case "disabled":
				api.accounts[0].CanTrade = false
			case "wrong-id":
				api.accounts[0].ID = 99
			}
			err := e.Arm(context.Background(), c, p, a)
			valid := kind == "practice" || kind == "combine" || kind == "express" || kind == "renamed"
			if !valid {
				if err == nil || len(api.requests) != 0 {
					t.Fatal("unsupported account armed", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(api.requests) != 0 {
				t.Fatal("arming placed an order")
			}
			if err := e.ExecuteAutomation(context.Background(), "swift", s, "mnq", 1); err != nil {
				t.Fatal(err)
			}
			if len(api.requests) != 1 || api.requests[0].AccountID != 42 {
				t.Fatal("wrong execution account")
			}
		})
	}
}

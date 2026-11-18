package admin

import (
	"fmt"
	"sort"

	"github.com/yelaco/projectx-pilot/internal/execution"
	"github.com/yelaco/projectx-pilot/internal/fleet"
)

func profileStatuses(views []fleet.View) map[string]execution.Status {
	out := map[string]execution.Status{}
	type totals struct {
		accounts, armed int
		single          string
	}
	counts := map[string]totals{}
	details := map[string]*execution.SubmissionDetail{}
	for _, r := range views {
		if r.ProfileID != "" {
			s := out[r.ProfileID]
			s.Automated = true
			s.AutomationProfileID = r.ProfileID
			s.RunProfileID = r.ProfileID
			s.RunRequested = s.RunRequested || r.Status.RunRequested || r.Status.RecoveryPending
			s.Armed = s.Armed || r.Status.Armed
			out[r.ProfileID] = s
			c := counts[r.ProfileID]
			c.accounts++
			if r.Status.Armed {
				c.armed++
			}
			c.single = r.Status.RunMessage
			counts[r.ProfileID] = c
		}
		for _, i := range r.Status.Intents {
			if i.ProfileID == "" {
				continue
			}
			s := out[i.ProfileID]
			s.Automated = true
			s.AutomationProfileID = i.ProfileID
			s.RunProfileID = i.ProfileID
			s.Intents = append(s.Intents, i)
			out[i.ProfileID] = s
			if r.Status.SubmissionDetail != nil && r.Status.SubmissionDetail.IntentID == i.ID {
				details[i.ID] = r.Status.SubmissionDetail
			}
		}
	}
	for id, s := range out {
		sort.SliceStable(s.Intents, func(a, b int) bool {
			if !s.Intents[a].Updated.Equal(s.Intents[b].Updated) {
				return s.Intents[a].Updated.After(s.Intents[b].Updated)
			}
			return s.Intents[a].ID < s.Intents[b].ID
		})
		for _, i := range s.Intents {
			if detail := details[i.ID]; detail != nil {
				s.SubmissionDetail = detail
				break
			}
		}
		c := counts[id]
		switch {
		case s.RunRequested && c.accounts == 1 && c.single != "" && c.single != "Paused":
			s.RunMessage = c.single
		case s.RunRequested:
			s.RunMessage = fmt.Sprintf("Running · %d of %d accounts armed; inspect each account’s readiness", c.armed, c.accounts)
		case s.Armed:
			s.RunMessage = fmt.Sprintf("Session armed · %d of %d accounts", c.armed, c.accounts)
		default:
			s.RunMessage = "Paused"
		}
		out[id] = s
	}
	return out
}
func displayExecutionMode(s execution.Status, views []fleet.View, configured bool) string {
	autoRequested, autoArmed, recovering := false, false, false
	for _, r := range views {
		autoRequested = autoRequested || r.Status.RunRequested
		recovering = recovering || r.Status.RecoveryPending
		autoArmed = autoArmed || r.Status.Armed
	}
	switch {
	case autoRequested || s.RunRequested:
		return "Automation running"
	case recovering || s.RecoveryPending:
		return "Automation waiting for recovery"
	case autoArmed || s.Armed && s.Automated:
		return "Automatic execution armed"
	case s.Armed:
		return "Practice execution armed"
	case configured:
		return "Execution configured · paused"
	default:
		return "Read-only mode"
	}
}

func summarizeManualExecution(s execution.Status, configured bool) ExecutionMonitor {
	out := ExecutionMonitor{Configured: configured, SubmissionDetail: s.SubmissionDetail, Armed: s.Armed, AccountID: s.AccountID, State: "idle", Message: s.Error}
	for _, i := range s.Intents {
		out.AccountID, out.State = i.AccountID, i.State
		out.Orders, out.Positions = len(i.ObservedOrders), len(i.ObservedPositions)
		out.ObservedAt = i.ObservedAt
		if out.Message == "" {
			out.Message = i.Message
		}
		break
	}
	return out
}

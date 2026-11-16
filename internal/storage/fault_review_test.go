package storage

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestFaultReviewOnlyUpdatesAcknowledgementAndPreservesRiskAndCAS(t *testing.T) {
	for _, mode := range []string{"valid", "latch", "state", "account", "payload race", "risk race", "other unresolved"} {
		t.Run(mode, func(t *testing.T) {
			s, err := Open(filepath.Join(t.TempDir(), "execution.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			reason := "local emergency flatten requested"
			values := map[string]any{"ID": "original", "AccountID": 42, "Session": "2026-11-13", "State": "closed", "HaltReason": reason, "LockReason": reason, "EmergencyAttempted": true, "Tag": "original-tag"}
			data, _ := json.Marshal(values)
			before := ExecRow{ID: "original", Account: "42", Session: "2026-11-13", State: "closed", Payload: string(data)}
			if err = s.ReserveExecution(before); err != nil {
				t.Fatal(err)
			}
			if err = s.LockExecution("42", before.Session, reason); err != nil {
				t.Fatal(err)
			}
			snapshot := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			values["AcknowledgedLockActive"] = true
			values["AcknowledgedLockReason"] = reason
			values["AcknowledgedLockSnapshot"] = snapshot
			values["AcknowledgedLockAt"] = time.Now().UTC()
			switch mode {
			case "latch":
				values["EmergencyAttempted"] = false
			case "state":
				values["State"] = "cancelled"
			case "account":
				values["AccountID"] = 43
			case "payload race":
				changed := before
				changed.Payload = before.Payload + " "
				_ = s.SaveExecution(changed)
			case "risk race":
				_ = s.LockExecution("42", before.Session, "daily loss limit reached")
			case "other unresolved":
				_ = s.ReserveExecution(ExecRow{ID: "other", Account: "42", Session: before.Session, State: "uncertain", Payload: "unresolved"})
			}
			after, _ := json.Marshal(values)
			err = s.ResolveFaultLock(before, string(after), reason, snapshot)
			if mode == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				if why, _ := s.ExecutionLock("42", before.Session); why != "" {
					t.Fatal("valid acknowledgement retained lock")
				}
				rows, _ := s.Executions()
				var got map[string]any
				_ = json.Unmarshal([]byte(rows[0].Payload), &got)
				if got["EmergencyAttempted"] != true || got["State"] != "closed" || got["Tag"] != "original-tag" {
					t.Fatal("execution history altered")
				}
			} else {
				if err == nil {
					t.Fatal("unsafe acknowledgement accepted", mode)
				}
				if why, _ := s.ExecutionLock("42", before.Session); why == "" {
					t.Fatal("failed acknowledgement partially cleared lock")
				}
			}
		})
	}
}
func TestRiskLockCannotBeReplacedByTechnicalProgress(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "execution.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.LockExecution("42", "2026-11-13", "daily loss limit reached")
	_ = s.LockExecution("42", "2026-11-13", "emergency management not verified flat; use TopstepX immediately")
	if reason, _ := s.ExecutionLock("42", "2026-11-13"); reason != "daily loss limit reached" {
		t.Fatal("risk lock overwritten", reason)
	}
}

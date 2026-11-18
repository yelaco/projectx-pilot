package storage

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestAutomaticRecoveryOnlyDeletesExactResolvedTechnicalLock(t *testing.T) {
	for _, mode := range []string{"valid", "risk", "legacy", "uncertain", "emergency", "original ambiguous", "open orders", "short interval", "stale interval", "payload race", "risk race", "other active", "newer closed", "audit failure"} {
		t.Run(mode, func(t *testing.T) {
			s, err := Open(filepath.Join(t.TempDir(), "execution.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			now := time.Now().UTC()
			reason := "position exposure differs from observed entry fills"
			values := map[string]any{"ID": "one", "AccountID": 42, "Session": "2026-11-13", "State": "closed", "HaltReason": reason, "LockReason": reason, "FlatSince": now.Add(-time.Second), "Updated": now}
			first, last := now, now.Add(3*time.Second)
			switch mode {
			case "risk":
				reason = "daily loss limit reached"
				values["LockReason"] = reason
			case "legacy":
				reason = "protective orders missing, pending or inconsistent"
				values["LockReason"] = reason
				values["HaltReason"] = reason
			case "uncertain":
				values["State"] = "uncertain"
			case "emergency":
				values["EmergencyAttempted"] = true
			case "original ambiguous":
				values["HaltReason"] = "entry cancellation uncertain"
			case "open orders":
				values["ObservedOrders"] = []any{map[string]any{"id": 77}}
			case "short interval":
				last = first.Add(time.Second)
			case "stale interval":
				first = now.Add(-time.Hour)
			}
			raw, _ := json.Marshal(values)
			before := ExecRow{ID: "one", Account: "42", Session: "2026-11-13", State: values["State"].(string), Payload: string(raw)}
			if err = s.ReserveExecution(before); err != nil {
				t.Fatal(err)
			}
			if err = s.LockExecution("42", before.Session, reason); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "payload race":
				changed := before
				changed.Payload += " "
				if err = s.SaveExecution(changed); err != nil {
					t.Fatal(err)
				}
			case "risk race":
				if err = s.LockExecution("42", before.Session, "daily loss limit reached"); err != nil {
					t.Fatal(err)
				}
			case "newer closed":
				if err = s.ReserveExecution(ExecRow{ID: "newer", Account: "42", Session: before.Session, State: "closed", Payload: "{}"}); err != nil {
					t.Fatal(err)
				}
			case "audit failure":
				if err = s.exec(`CREATE TABLE execution_lock_recoveries(id INTEGER PRIMARY KEY,account TEXT,session TEXT,intent TEXT,reason TEXT,first_observed TEXT,last_observed TEXT,at TEXT); CREATE TRIGGER fail_recovery BEFORE INSERT ON execution_lock_recoveries BEGIN SELECT RAISE(ABORT,'test'); END;`); err != nil {
					t.Fatal(err)
				}
			case "other active":
				if err = s.ReserveExecution(ExecRow{ID: "other", Account: "42", Session: before.Session, State: "uncertain", Payload: "{}"}); err != nil {
					t.Fatal(err)
				}
			}
			err = s.ResolveRecoveredExecutionLock(before, reason, first, last)
			why, _ := s.ExecutionLock("42", before.Session)
			if mode == "valid" {
				if err != nil || why != "" {
					t.Fatal("valid recovery denied", why, err)
				}
				rows, _ := s.Executions()
				if len(rows) != 1 || rows[0].Payload != before.Payload {
					t.Fatal("recovery altered execution evidence")
				}
			} else if err == nil || why == "" {
				t.Fatal("unsafe recovery accepted", mode)
			}
		})
	}
}

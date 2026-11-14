package signal

import (
	"testing"
	"time"
)

func TestExitSchemaCannotBecomeEntry(t *testing.T) {
	now := time.Now().UTC()
	s := Signal{ID: "exit-1", Action: "sl", TradeID: "entry-1", Strategy: "swift", Symbol: "MNQ", Direction: "BUY", Timestamp: now}
	if err := s.ValidateSchema(now, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(now, time.Minute, .25); err == nil {
		t.Fatal("exit accepted as entry")
	}
	for _, mutate := range []func(*Signal){func(s *Signal) { s.TradeID = "" }, func(s *Signal) { s.TradeID = s.ID }, func(s *Signal) { s.TradeID = "<ORIGINAL_ENTRY_SIGNAL_ID>" }, func(s *Signal) { s.Action = "close-any" }, func(s *Signal) { s.Timestamp = now.Add(-time.Hour) }, func(s *Signal) { s.Entry = 20000 }} {
		bad := s
		mutate(&bad)
		if bad.ValidateSchema(now, time.Minute) == nil {
			t.Fatal("invalid exit accepted")
		}
	}
}

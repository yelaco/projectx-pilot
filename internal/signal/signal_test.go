package signal

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func valid(now time.Time) Signal {
	return Signal{ID: "swift-001", Strategy: "swift-mnq-scalping", Symbol: "MNQ", Direction: "BUY", Entry: 20000, StopLoss: 19995, TakeProfit: 20010, Timestamp: now}
}
func TestValidation(t *testing.T) {
	now := time.Now().UTC()
	s := valid(now)
	if err := s.Validate(now, 5*time.Minute, .25); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		change func(*Signal)
	}{
		{"missing ID", func(s *Signal) { s.ID = "" }}, {"future", func(s *Signal) { s.Timestamp = now.Add(time.Minute) }}, {"expired", func(s *Signal) { s.Timestamp = now.Add(-6 * time.Minute) }}, {"missing time", func(s *Signal) { s.Timestamp = time.Time{} }},
		{"wrong stop", func(s *Signal) { s.StopLoss = 20005 }}, {"wrong target", func(s *Signal) { s.TakeProfit = 19990 }}, {"bad precision", func(s *Signal) { s.Entry = 20000.1 }}, {"unknown direction", func(s *Signal) { s.Direction = "LONG" }}, {"negative", func(s *Signal) { s.Entry = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := valid(now)
			tc.change(&s)
			if s.Validate(now, 5*time.Minute, .25) == nil {
				t.Fatal("invalid signal accepted")
			}
		})
	}
	s = valid(now)
	s.Direction = "SELL"
	s.StopLoss = 20005
	s.TakeProfit = 19990
	if err := s.Validate(now, 5*time.Minute, .25); err != nil {
		t.Fatal(err)
	}
}
func TestStrictJSON(t *testing.T) {
	b, _ := json.Marshal(valid(time.Now().UTC()))
	if _, err := Parse(string(b)); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{`{"id":"one","id":"two"}`, `{"account_id":123}`, `{"entry":"20000"}`, `null`, string(b) + ` {}`, strings.Repeat("x", 4097)} {
		s, err := Parse(p)
		if err == nil {
			err = s.Validate(time.Now(), time.Minute, .25)
		}
		if err == nil {
			t.Fatal("invalid JSON/schema accepted")
		}
	}
}

func TestTradingViewContinuousTickerInID(t *testing.T) {
	now := time.Now().UTC()
	s := valid(now)
	s.ID = "swift-mnq-buy-MNQ1!-2026-11-13T09:00:00Z"
	if err := s.ValidateSchema(now, time.Minute); err != nil {
		t.Fatal(err)
	}
}

func TestExitActionsAreUnsupported(t *testing.T) {
	for _, action := range []string{"tp", "sl"} {
		if _, err := Parse(`{"id":"exit","action":"` + action + `","strategy":"swift","symbol":"MNQ","timestamp":"2026-11-14T12:00:00Z"}`); err == nil {
			t.Fatal("retired exit alert accepted")
		}
	}
}

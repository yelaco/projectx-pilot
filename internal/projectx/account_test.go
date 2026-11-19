package projectx

import (
	"encoding/json"
	"testing"
)

func TestAccountBalancePresenceSurvivesDecode(t *testing.T) {
	for _, tc := range []struct {
		body  string
		known bool
		value float64
	}{{`{"id":42,"name":"offline","balance":0,"simulated":true}`, true, 0}, {`{"id":42,"balance":50000}`, true, 50000}, {`{"id":42}`, false, 0}, {`{"id":42,"balance":null}`, false, 0}} {
		var a Account
		if err := json.Unmarshal([]byte(tc.body), &a); err != nil {
			t.Fatal(err)
		}
		if a.ID != 42 || a.BalanceKnown != tc.known || a.Balance != tc.value {
			t.Fatal("incorrect balance presence", a)
		}
	}
}

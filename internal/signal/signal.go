// Package signal validates imported alerts without authorizing execution.
package signal

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"time"
)

type Signal struct {
	ID         string            `json:"id"`
	Strategy   string            `json:"strategy"`
	Symbol     string            `json:"symbol"`
	Direction  string            `json:"direction"`
	Entry      float64           `json:"entry"`
	StopLoss   float64           `json:"stop_loss"`
	TakeProfit float64           `json:"take_profit"`
	Timestamp  time.Time         `json:"timestamp"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

func Parse(payload string) (Signal, error) {
	var s Signal
	if len(payload) > 4096 {
		return s, errors.New("alert JSON exceeds 4 KiB")
	}
	// Reject ambiguous duplicate top-level keys rather than accepting the last value.
	scan := json.NewDecoder(strings.NewReader(payload))
	first, err := scan.Token()
	if err != nil || first != json.Delim('{') {
		return s, errors.New("alert must be a JSON object")
	}
	seen := map[string]bool{}
	for scan.More() {
		key, err := scan.Token()
		if err != nil {
			return s, errors.New("invalid alert JSON")
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return s, errors.New("duplicate or invalid alert field")
		}
		seen[name] = true
		var value json.RawMessage
		if scan.Decode(&value) != nil {
			return s, errors.New("invalid alert JSON")
		}
	}
	d := json.NewDecoder(strings.NewReader(payload))
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		return s, errors.New("invalid alert JSON: check field names, numeric prices and RFC3339 timestamp")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return s, errors.New("alert must contain exactly one JSON object")
	}
	return s, nil
}
func (s Signal) ValidateSchema(now time.Time, maxAge time.Duration) error {
	if len(s.ID) < 1 || len(s.ID) > 128 {
		return errors.New("signal ID must contain 1..128 characters")
	}
	for _, r := range s.ID {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:!", r)) {
			return errors.New("signal ID must be alphanumeric with hyphens, underscores, dots, colons or exclamation marks")
		}
	}
	if s.Strategy == "" || len(s.Strategy) > 100 || s.Symbol == "" || len(s.Symbol) > 16 {
		return errors.New("strategy and symbol are required")
	}
	for _, r := range s.Symbol {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return errors.New("symbol must be an uppercase futures root such as MNQ")
		}
	}
	if s.Timestamp.IsZero() || s.Timestamp.After(now.Add(30*time.Second)) {
		return errors.New("signal timestamp is missing or too far in the future")
	}
	if now.Sub(s.Timestamp) > maxAge {
		return errors.New("signal has expired; do not reuse an old alert")
	}
	for _, p := range []float64{s.Entry, s.StopLoss, s.TakeProfit} {
		if p <= 0 || math.IsNaN(p) || math.IsInf(p, 0) {
			return errors.New("prices must be finite and positive")
		}
	}
	switch s.Direction {
	case "BUY":
		if !(s.StopLoss < s.Entry && s.TakeProfit > s.Entry) {
			return errors.New("BUY needs stop_loss below entry and take_profit above entry")
		}
	case "SELL":
		if !(s.StopLoss > s.Entry && s.TakeProfit < s.Entry) {
			return errors.New("SELL needs stop_loss above entry and take_profit below entry")
		}
	default:
		return errors.New("direction must be BUY or SELL")
	}
	if len(s.Metadata) > 16 {
		return errors.New("maximum 16 metadata entries")
	}
	for k, v := range s.Metadata {
		if len(k) > 64 || len(v) > 256 {
			return errors.New("metadata exceeds size limits")
		}
	}
	return nil
}

// Validate also checks authoritative tick alignment; schema-only validation is
// suitable for the unassigned delivery inbox, never execution.
func (s Signal) Validate(now time.Time, maxAge time.Duration, tick float64) error {
	if err := s.ValidateSchema(now, maxAge); err != nil {
		return err
	}
	if tick <= 0 || math.IsNaN(tick) || math.IsInf(tick, 0) {
		return errors.New("authoritative tick metadata is unavailable")
	}
	for _, p := range []float64{s.Entry, s.StopLoss, s.TakeProfit} {
		ticks := p / tick
		if math.Abs(ticks-math.Round(ticks)) > 1e-6 {
			return errors.New("prices must align to the selected contract's tick size")
		}
	}
	return nil
}

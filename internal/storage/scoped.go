package storage

import (
	"encoding/json"
	"errors"
)

// ScopedExecution uses one shared SQLite transaction boundary while isolating
// recovery observations. Manual and automatic monitors never own the same intent.
type ScopedExecution struct {
	*Inbox
	Account string
	Manual  bool
}

func (s ScopedExecution) Executions() ([]ExecRow, error) {
	account := ""
	if !s.Manual {
		account = s.Account
	}
	rows, err := s.Inbox.executionRows(account)
	if err != nil {
		return nil, err
	}
	out := []ExecRow{}
	for _, r := range rows {
		var i struct {
			Source string `json:"source"`
		}
		if json.Unmarshal([]byte(r.Payload), &i) != nil {
			return nil, errors.New("invalid execution recovery record")
		}
		if s.Manual {
			if i.Source != "tradingview" {
				out = append(out, r)
			}
		} else if r.Account == s.Account && i.Source == "tradingview" {
			out = append(out, r)
		}
	}
	return out, nil
}

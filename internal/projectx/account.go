package projectx

import "encoding/json"

// UnmarshalJSON preserves whether balance was explicitly observed. An omitted
// or null balance cannot support public lifetime-performance reconciliation.
func (a *Account) UnmarshalJSON(data []byte) error {
	type accountFields Account
	var wire struct {
		accountFields
		Balance *float64 `json:"balance"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*a = Account(wire.accountFields)
	a.BalanceKnown = wire.Balance != nil
	if wire.Balance != nil {
		a.Balance = *wire.Balance
	}
	return nil
}

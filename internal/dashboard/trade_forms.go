package dashboard

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/yelaco/projectx-pilot/internal/risk"
	"github.com/yelaco/projectx-pilot/internal/session"
)

func parseTradeManager(r *http.Request) (risk.Profile, error) {
	p := risk.Profile{ID: r.PostForm.Get("id"), Name: r.PostForm.Get("name"), MultiAccountMode: r.PostForm.Get("multi_account_mode"), Routing: r.PostForm.Get("routing")}
	seen := map[int64]bool{}
	values := append([]string(nil), r.PostForm["account_ids"]...)
	values = append(values, strings.FieldsFunc(r.PostForm.Get("account_ids_csv"), func(r rune) bool { return r == ',' || r == ' ' || r == '\n' })...)
	for _, v := range values {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			return p, errors.New("select valid positive account IDs")
		}
		if !seen[id] {
			p.AccountIDs = append(p.AccountIDs, id)
			seen[id] = true
		}
	}
	if r.PostForm.Has("account_risk_overrides_present") || r.PostForm.Has("account_risk_overrides") {
		p.AccountRiskOverrides = map[int64]float64{}
	}
	addOverride := func(accountText, valueText string) error {
		if strings.TrimSpace(valueText) == "" {
			return nil
		}
		account, err := strconv.ParseInt(strings.TrimSpace(accountText), 10, 64)
		if err != nil || account <= 0 {
			return errors.New("account risk override needs a positive account ID")
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(valueText), 64)
		if err != nil {
			return errors.New("account risk override must be numeric")
		}
		if p.AccountRiskOverrides == nil {
			p.AccountRiskOverrides = map[int64]float64{}
		}
		if _, exists := p.AccountRiskOverrides[account]; exists {
			return errors.New("duplicate account risk override")
		}
		p.AccountRiskOverrides[account] = value
		return nil
	}
	for key, values := range r.PostForm {
		if strings.HasPrefix(key, "account_risk_") && key != "account_risk_overrides" && key != "account_risk_overrides_present" {
			if len(values) != 1 {
				return p, errors.New("duplicate account risk override field")
			}
			if err := addOverride(strings.TrimPrefix(key, "account_risk_"), values[0]); err != nil {
				return p, err
			}
		}
	}
	for _, line := range strings.FieldsFunc(r.PostForm.Get("account_risk_overrides"), func(r rune) bool { return r == '\n' || r == ',' || r == ';' }) {
		parts := strings.Split(line, "=")
		if len(parts) != 2 {
			return p, errors.New("use account ID=maximum risk for each override")
		}
		if err := addOverride(parts[0], parts[1]); err != nil {
			return p, err
		}
	}
	p.PerTradeOnly = true
	var err error
	p.MaxRiskPerTrade, err = strconv.ParseFloat(r.PostForm.Get("max_risk_per_trade"), 64)
	if err != nil {
		return p, errors.New("maximum risk per trade must be numeric")
	}
	if r.PostForm.Has("session_start") || r.PostForm.Has("session_last_entry") || r.PostForm.Has("session_flatten") {
		p.Schedule = &session.Schedule{Start: r.PostForm.Get("session_start"), LastEntry: r.PostForm.Get("session_last_entry"), Flatten: r.PostForm.Get("session_flatten")}
	}
	return p, p.Validate()
}

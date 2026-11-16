package dashboard

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/yelaco/projectx-pilot/internal/risk"
)

func parseTradeManager(r *http.Request) (risk.Profile, error) {
	p := risk.Profile{ID: r.PostForm.Get("id"), Name: r.PostForm.Get("name"), Routing: r.PostForm.Get("routing")}
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
	p.PerTradeOnly = true
	var err error
	p.MaxRiskPerTrade, err = strconv.ParseFloat(r.PostForm.Get("max_risk_per_trade"), 64)
	if err != nil {
		return p, errors.New("maximum risk per trade must be numeric")
	}
	return p, p.Validate()
}

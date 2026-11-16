package dashboard

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yelaco/projectx-pilot/internal/execution"
	"github.com/yelaco/projectx-pilot/internal/projectx"
)

func TestStaleQuoteExposurePromptShowsSeparateAcknowledgementAndNoFlatten(t *testing.T) {
	for _, action := range []string{"/execution/review-stale-quote-lock", "/fleet/review-stale-quote"} {
		var body strings.Builder
		v := view{CSRF: "csrf-test", StaleQuoteConfirmationAction: action, StaleQuoteConfirmation: &execution.StaleQuoteConfirmationRequired{AccountID: 42, IntentID: "closed-stale", Token: "scoped-test-token", Positions: []projectx.Position{{ID: 100, ContractID: "mnq", Size: 1, Type: 1, AveragePrice: 20000}}, Orders: []projectx.Order{{ID: 101, ContractID: "mnq", Size: 1}}}}
		if err := page.ExecuteTemplate(&body, "stale-quote-confirmation", v); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`action="` + action + `"`, `name="exposure_confirmed"`, `name="exposure_confirmation" value="scoped-test-token"`, "Account #42", "1 open position(s), 1 working order(s)", "current_password", "Leave them untouched"} {
			if !strings.Contains(body.String(), want) {
				t.Fatal("missing explicit confirmation", want)
			}
		}
		if strings.Contains(body.String(), `action="/execution/flatten"`) || strings.Contains(body.String(), `action="/fleet/account-flatten"`) {
			t.Fatal("review prompt includes closing action")
		}
	}
}
func TestStaleQuoteExposureCheckboxIsEnforcedServerSide(t *testing.T) {
	r := &http.Request{PostForm: url.Values{"exposure_confirmation": {"snapshot-token"}}}
	if _, err := staleQuoteConfirmationToken(r); err == nil {
		t.Fatal("hidden token bypassed checkbox")
	}
	r.PostForm.Set("exposure_confirmed", "yes")
	if token, err := staleQuoteConfirmationToken(r); err != nil || token != "snapshot-token" {
		t.Fatal("confirmed token not forwarded", err)
	}
}

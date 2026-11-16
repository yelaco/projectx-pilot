package dashboard

import (
	"net/url"
	"strings"
	"testing"

	"github.com/yelaco/projectx-pilot/internal/execution"
)

func TestGeneralFaultReviewRequiresCSRFPasswordAndAcknowledgement(t *testing.T) {
	_, password, h := managementFixture(t)
	csrf := formCSRF(t, requestAs(h, "local", password, "GET", "/execution", nil))
	for _, route := range []string{"/execution/review-fault-lock", "/fleet/review-fault"} {
		fields := url.Values{"csrf": {"invalid"}, "account_id": {"42"}, "intent_id": {"fault"}, "current_password": {password}}
		if w := requestAs(h, "local", password, "POST", route, fields); w.Code != 403 {
			t.Fatal("CSRF bypass", route, w.Code)
		}
		fields.Set("csrf", csrf)
		fields.Set("current_password", "wrong")
		if w := requestAs(h, "local", password, "POST", route, fields); w.Code != 400 || !strings.Contains(w.Body.String(), "current administrator password required") {
			t.Fatal("password bypass", route, w.Code)
		}
		fields.Set("current_password", password)
		fields.Set("exposure_confirmation", "token")
		if w := requestAs(h, "local", password, "POST", route, fields); w.Code != 400 || !strings.Contains(w.Body.String(), "confirm that existing positions") {
			t.Fatal("acknowledgement checkbox bypass", route, w.Code)
		}
	}
}
func TestGeneralFaultPromptShowsExactReasonEvenWhenFlat(t *testing.T) {
	var body strings.Builder
	v := view{CSRF: "test", StaleQuoteConfirmationAction: "/execution/review-fault-lock", StaleQuoteConfirmation: &execution.StaleQuoteConfirmationRequired{Reason: "session costs or P&L cannot be verified", AccountID: 42, IntentID: "fault", Token: "nonce"}}
	if err := page.ExecuteTemplate(&body, "stale-quote-confirmation", v); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Confirm account lock review", "session costs or P&amp;L cannot be verified", "0 open position(s)", `action="/execution/review-fault-lock"`, `name="exposure_confirmed"`} {
		if !strings.Contains(body.String(), want) {
			t.Fatal("missing reason-scoped confirmation", want)
		}
	}
	if strings.Contains(body.String(), "obsolete stale-quote lock") {
		t.Fatal("general fault mislabeled stale quote")
	}
}

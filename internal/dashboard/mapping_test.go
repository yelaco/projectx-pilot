package dashboard

import (
	"net/url"
	"projectxbot/internal/admin"
	"strings"
	"testing"
)

func TestDirectionalMappingFormAndGeneratedMessages(t *testing.T) {
	m, password, h := managementFixture(t)
	if err := m.SaveAutomation(admin.Automation{ID: "swift", Name: "Swift", Strategy: "swift", AccountID: 42, Instruments: []string{"MNQ"}, Source: "tradingview-manual"}); err != nil {
		t.Fatal(err)
	}
	path := "/automations/profiles/swift?tab=webhook"
	page := requestAs(h, "local", password, "GET", path, nil)
	csrf := formCSRF(t, page)
	if !strings.Contains(page.Body.String(), "Shared placeholders for Buy and Sell") || !strings.Contains(page.Body.String(), `name="buy_entry"`) || !strings.Contains(page.Body.String(), `name="sell_entry"`) {
		t.Fatal("mapping controls missing")
	}
	values := url.Values{"csrf": {csrf}, "id": {"swift"}, "mapping_mode": {"separate"}, "buy_entry": {"{{close}}"}, "buy_stop": {"{{plot_0}}"}, "buy_target": {"{{plot_1}}"}, "sell_entry": {"{{open}}"}, "sell_stop": {"{{plot_2}}"}, "sell_target": {"{{plot_3}}"}}
	if w := requestAs(h, "local", password, "POST", "/mapping", values); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	route := m.Ingress().Routes[0]
	if route.MappingMode != "separate" || route.BuyMapping.Stop != "{{plot_0}}" || route.SellMapping.Stop != "{{plot_2}}" {
		t.Fatal(route)
	}
	page = requestAs(h, "local", password, "GET", path, nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `value="separate" selected`) || !strings.Contains(page.Body.String(), `data-mapping-group="shared" disabled hidden`) {
		t.Fatal("saved mode not rendered", page.Body.String())
	}
	messages := requestAs(h, "local", password, "GET", "/automations/profiles/swift?tab=messages", nil)
	if messages.Code != 200 || strings.Contains(messages.Body.String(), "CONFIGURE_TARGET_PLOT") {
		t.Fatal("ready messages not rendered")
	}
	values.Set("sell_entry", "not-a-placeholder")
	if w := requestAs(h, "local", password, "POST", "/mapping", values); w.Code != 400 {
		t.Fatal("invalid mapping accepted", w.Code)
	}
}

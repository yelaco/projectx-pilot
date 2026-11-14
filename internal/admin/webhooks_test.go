package admin

import (
	"encoding/json"
	"projectxbot/internal/ingress"
	"projectxbot/internal/signal"
	"strings"
	"testing"
	"time"
)

func TestProfileCreatesPersistentWebhookAndTemplates(t *testing.T) {
	m, _, p, state := setup(t)
	a := Automation{ID: "swift", Name: "Swift", Strategy: "swift-mnq-scalping", AccountID: 42, Instruments: []string{"MNQ"}, Source: "tradingview-manual"}
	if err := m.SaveAutomation(a); err != nil {
		t.Fatal(err)
	}
	routes := m.Ingress().Routes
	if len(routes) != 1 || len(routes[0].Token) != 64 || m.Ingress().Enabled {
		t.Fatal("bad generated default", routes)
	}
	token := routes[0].Token
	if err := m.SaveAutomation(a); err != nil {
		t.Fatal(err)
	}
	if m.Ingress().Routes[0].Token != token {
		t.Fatal("editing rotated token")
	}
	if err := m.SaveIngress(true, false, "https://example.test"); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveMapping("swift", "{{close}}", "{{plot_0}}", "{{plot_1}}", false); err != nil {
		t.Fatal(err)
	}
	templates := m.Templates()
	if len(templates) != 1 || !templates[0].Ready || templates[0].URL != "https://example.test/tradingview/swift" {
		t.Fatal(templates)
	}
	text := strings.NewReplacer("{{close}}", "20000", "{{plot_0}}", "19995", "{{plot_1}}", "20010", "{{time}}", "2026-11-13T09:00:00Z", "{{timenow}}", time.Now().UTC().Format(time.RFC3339)).Replace(templates[0].Buy)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatal("generated template is invalid after substitution", err)
	}
	if parsed["delivery_token"] != token {
		t.Fatal("template lacks generated credential")
	}
	if m.SaveMapping("swift", "$(shell)", "", "", false) == nil {
		t.Fatal("arbitrary mapping accepted")
	}
	if m.SaveIngress(true, false, "https://user:password@example.test") == nil {
		t.Fatal("credentials in public URL accepted")
	}
	c := m.Settings().Config
	next, _, err := Open(c, p, state)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if next.Ingress().Routes[0].Token != token {
		t.Fatal("restart changed token")
	}
	if err = m.SaveMapping("swift", "{{close}}", "{{plot_0}}", "{{plot_1}}", true); err != nil {
		t.Fatal(err)
	}
	if m.Ingress().Routes[0].Token == token {
		t.Fatal("rotation failed")
	}
	if err = m.DeleteAutomation("swift"); err != nil {
		t.Fatal(err)
	}
	if len(m.Ingress().Routes) != 0 {
		t.Fatal("deletion left active capability")
	}
}

func TestQuickPresetIsIdempotentAndDoesNotArmTrading(t *testing.T) {
	m, _, _, _ := setup(t)
	a := Automation{ID: "swift", Name: "Swift", Strategy: "swift", AccountID: 42, Instruments: []string{"MNQ"}, Source: "tradingview-manual"}
	if err := m.SaveAutomation(a); err != nil {
		t.Fatal(err)
	}
	token := m.Ingress().Routes[0].Token
	if err := m.ApplyIngressPreset("unknown"); err == nil || m.Ingress().Enabled {
		t.Fatal("unknown preset changed receiving")
	}
	if err := m.SaveIngress(true, false, "https://old-origin.example"); err != nil {
		t.Fatal(err)
	}
	if err := m.ApplyIngressPreset("cloudflare-quick"); err != nil {
		t.Fatal(err)
	}
	s := m.Ingress()
	if !s.Enabled || !s.QuickTunnel || s.PublicURL != "" {
		t.Fatal("preset failed or retained stale origin", s)
	}
	if err := m.ApplyIngressPreset("cloudflare-quick"); err != nil {
		t.Fatal(err)
	}
	if m.Ingress().Routes[0].Token != token || m.Settings().Config.Mode != "read-only" || len(m.Policies().Accounts) != 0 {
		t.Fatal("preset changed a token, trading mode or account grants")
	}
	if err := m.ApplyIngressPreset("disabled"); err != nil {
		t.Fatal(err)
	}
	s = m.Ingress()
	if s.Enabled || s.QuickTunnel || s.PublicURL != "" || s.Routes[0].Token != token {
		t.Fatal("disable preset failed")
	}
}

func TestSeparateMappingsSelectDirectionAndSurviveModeSwitchAndRestart(t *testing.T) {
	m, _, path, state := setup(t)
	automation := Automation{ID: "directional", Name: "Directional", Strategy: "manual-plots", AccountID: 42, Instruments: []string{"MNQ"}, Source: "tradingview-manual"}
	if err := m.SaveAutomation(automation); err != nil {
		t.Fatal(err)
	}
	// Existing routes have no explicit mode and must keep their shared behavior.
	legacy := m.Ingress().Routes[0]
	if legacy.MappingMode != "" || legacy.Mapping("BUY").Entry != "{{close}}" || legacy.Mapping("SELL").Entry != "{{close}}" {
		t.Fatal("legacy default changed")
	}
	shared := ingress.PriceMapping{Entry: "{{close}}", Stop: "{{plot_0}}", Target: "{{plot_1}}"}
	buy := ingress.PriceMapping{Entry: "{{open}}", Stop: `{{plot("Buy Stop")}}`, Target: "{{plot_3}}"}
	sell := ingress.PriceMapping{Entry: "{{high}}", Stop: "{{plot_4}}", Target: "{{plot_5}}"}
	if err := m.SaveMappings("directional", "shared", shared, ingress.PriceMapping{}, ingress.PriceMapping{}, false); err != nil {
		t.Fatal(err)
	}
	token := m.Ingress().Routes[0].Token
	if err := m.SaveMappings("directional", "separate", ingress.PriceMapping{}, buy, sell, false); err != nil {
		t.Fatal(err)
	}
	templates := m.Templates()
	msg := templates[0]
	if !msg.Ready || !msg.BuyReady || !msg.SellReady || !strings.Contains(msg.Buy, buy.Stop) || strings.Contains(msg.Buy, sell.Stop) || !strings.Contains(msg.Sell, sell.Stop) || strings.Contains(msg.Sell, buy.Stop) {
		t.Fatal("directions mixed", msg)
	}
	// Both drafts must become valid receiver JSON after numeric placeholder substitution.
	for _, body := range []string{msg.Buy, msg.Sell} {
		converted := strings.NewReplacer(buy.Entry, "20000", buy.Stop, "19990", buy.Target, "20010", sell.Entry, "20000", sell.Stop, "20010", sell.Target, "19990", "{{time}}", "2026-11-13T12:00:00Z", "{{timenow}}", "2026-11-13T12:00:00Z").Replace(body)
		// A map retains the actual receiver field names, independently of the template struct.
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal([]byte(converted), &envelope); err != nil {
			t.Fatal(err)
		}
		var sig map[string]any
		if err := json.Unmarshal(envelope["signal"], &sig); err != nil {
			t.Fatal(err)
		}
		if sig["entry"] != float64(20000) || sig["direction"] == "BUY" && sig["stop_loss"] != float64(19990) || sig["direction"] == "SELL" && sig["stop_loss"] != float64(20010) {
			t.Fatal(sig)
		}
	}
	if err := m.SaveMapping("directional", shared.Entry, shared.Stop, shared.Target, false); err != nil {
		t.Fatal(err)
	}
	route := m.Ingress().Routes[0]
	if route.BuyMapping != buy || route.SellMapping != sell || route.Mapping("BUY") != shared || route.Mapping("SELL") != shared {
		t.Fatal("mode switch lost saved mappings", route)
	}
	if err := m.SaveMappings("directional", "separate", ingress.PriceMapping{}, route.BuyMapping, route.SellMapping, false); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveAutomation(automation); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	next, _, err := Open(m.Settings().Config, path, state)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	route = next.Ingress().Routes[0]
	if route.MappingMode != "separate" || route.BuyMapping != buy || route.SellMapping != sell || route.Token != token {
		t.Fatal("restart or profile sync changed mappings")
	}
}

func TestSeparateMappingValidationAndIndependentReadiness(t *testing.T) {
	m, _, _, _ := setup(t)
	if err := m.SaveAutomation(Automation{ID: "plots", Name: "Plots", Strategy: "plots", AccountID: 42, Instruments: []string{"MNQ"}, Source: "tradingview-manual"}); err != nil {
		t.Fatal(err)
	}
	buy := ingress.PriceMapping{Entry: "{{close}}", Stop: "{{plot_0}}", Target: "{{plot_1}}"}
	sell := ingress.PriceMapping{Entry: "{{close}}", Stop: "{{plot_2}}"}
	if err := m.SaveMappings("plots", "separate", ingress.PriceMapping{}, buy, sell, false); err != nil {
		t.Fatal(err)
	}
	template := m.Templates()[0]
	if template.Ready || !template.BuyReady || template.SellReady || !strings.Contains(template.Sell, "CONFIGURE_TARGET_PLOT") || strings.Contains(template.Buy, "CONFIGURE_TARGET_PLOT") {
		t.Fatal("readiness mixed directions", template)
	}
	before := m.Ingress().Routes[0]
	invalid := sell
	invalid.Entry = "$(shell)"
	if m.SaveMappings("plots", "separate", ingress.PriceMapping{}, buy, invalid, true) == nil {
		t.Fatal("invalid sell placeholder accepted")
	}
	if m.SaveMappings("plots", "other", buy, buy, sell, true) == nil {
		t.Fatal("invalid mode accepted")
	}
	if m.SaveMappings("plots", "separate", ingress.PriceMapping{}, buy, ingress.PriceMapping{}, false) == nil {
		t.Fatal("missing sell entry accepted")
	}
	after := m.Ingress().Routes[0]
	if after.Token != before.Token || after.MappingMode != before.MappingMode || after.BuyMapping != before.BuyMapping || after.SellMapping != before.SellMapping {
		t.Fatal("failed save mutated state")
	}
}

func TestExitDraftRequiresOriginalEntryReference(t *testing.T) {
	m, _, _, _ := setup(t)
	if err := m.SaveAutomation(Automation{ID: "swift", Name: "Swift", Strategy: "swift-mnq-scalping", AccountID: 42, Instruments: []string{"MNQ"}, Source: "tradingview-manual"}); err != nil {
		t.Fatal(err)
	}
	drafts := m.Templates()[0]
	for _, draft := range []string{drafts.BuyTP, drafts.BuySL, drafts.SellTP, drafts.SellSL} {
		now := time.Now().UTC()
		text := strings.ReplaceAll(draft, "{{timenow}}", now.Format(time.RFC3339))
		var body struct {
			Signal signal.Signal `json:"signal"`
		}
		if err := json.Unmarshal([]byte(text), &body); err != nil {
			t.Fatal(err)
		}
		if body.Signal.ValidateSchema(now, time.Minute) == nil {
			t.Fatal("unconfigured reference accepted")
		}
		body.Signal.TradeID = "original-entry"
		if err := body.Signal.ValidateSchema(now, time.Minute); err != nil {
			t.Fatal(err)
		}
		if !body.Signal.IsExit() {
			t.Fatal("draft is not an exit")
		}
	}
}

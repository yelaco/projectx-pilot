package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"projectxbot/internal/ingress"
)

func (m *Manager) syncIngressLocked() error {
	return m.ingress.Update(func(s *ingress.Settings) error {
		old := map[string]ingress.Route{}
		for _, r := range s.Routes {
			old[r.ID] = r
		}
		routes := []ingress.Route{}
		for _, a := range m.policies.Automations {
			if a.Source != "tradingview-manual" {
				continue
			}
			r := old[a.ID]
			if r.Token == "" {
				token, err := ingress.Token()
				if err != nil {
					return err
				}
				r = ingress.Route{Token: token, Entry: "{{close}}"}
			}
			r.ID = a.ID
			r.Strategy = a.Strategy
			r.Symbols = append([]string(nil), a.Instruments...)
			routes = append(routes, r)
		}
		s.Routes = routes
		return nil
	})
}
func (m *Manager) Ingress() ingress.Settings { return m.ingress.Snapshot() }
func (m *Manager) SaveIngress(enabled, quick bool, publicURL string) error {
	publicURL = strings.TrimRight(strings.TrimSpace(publicURL), "/")
	if !ingress.PublicURL(publicURL) {
		return errors.New("public URL must be an HTTPS origin without credentials, path, query or fragment")
	}
	if m.executor != nil {
		m.executor.Pause()
	}
	err := m.ingress.Update(func(s *ingress.Settings) error {
		nextQuick := enabled && quick
		if nextQuick && !s.QuickTunnel {
			s.PublicURL = ""
		}
		s.Enabled = enabled
		s.QuickTunnel = nextQuick
		if !s.QuickTunnel {
			s.PublicURL = publicURL
		}
		return nil
	})
	if err == nil && m.wakeTunnel != nil {
		m.wakeTunnel()
	}
	return err
}

// SaveMapping preserves the legacy shared-mapping operation.
func (m *Manager) SaveMapping(id, entry, stop, target string, rotate bool) error {
	return m.SaveMappings(id, "shared", ingress.PriceMapping{Entry: entry, Stop: stop, Target: target}, ingress.PriceMapping{}, ingress.PriceMapping{}, rotate)
}
func (m *Manager) SaveMappings(id, mode string, shared, buy, sell ingress.PriceMapping, rotate bool) error {
	if mode != "shared" && mode != "separate" {
		return errors.New("mapping mode must be shared or separate")
	}
	valid := func(p ingress.PriceMapping) bool {
		return p.Entry != "" && ingress.Expression(p.Entry) && ingress.Expression(p.Stop) && ingress.Expression(p.Target)
	}
	if mode == "shared" && !valid(shared) || mode == "separate" && (!valid(buy) || !valid(sell)) {
		return errors.New("each active mapping needs a verified entry placeholder; use verified TradingView price placeholders and leave unknown stop/target mappings empty")
	}
	if m.executor != nil {
		m.executor.Pause()
	}
	return m.ingress.Update(func(s *ingress.Settings) error {
		for i := range s.Routes {
			r := &s.Routes[i]
			if r.ID != id {
				continue
			}
			r.MappingMode = mode
			if mode == "shared" {
				r.Entry, r.Stop, r.Target = shared.Entry, shared.Stop, shared.Target
			} else {
				r.BuyMapping, r.SellMapping = buy, sell
			}
			if rotate {
				token, err := ingress.Token()
				if err != nil {
					return err
				}
				r.Token = token
			}
			return nil
		}
		return errors.New("TradingView profile not found")
	})
}

type Template struct {
	ProfileID, Symbol, URL, Buy, Sell string
	Ready                             bool
	BuyReady, SellReady               bool
}

func (m *Manager) Templates() []Template {
	s := m.Ingress()
	urls := s.WebhookURLs()
	var out []Template
	for _, r := range s.Routes {
		for _, symbol := range r.Symbols {
			build := func(direction string) string {
				mapping := r.Mapping(direction)
				entry, stop, target := mapping.Entry, mapping.Stop, mapping.Target
				if entry == "" {
					entry = `"CONFIGURE_ENTRY_PLOT"`
				}
				if stop == "" {
					stop = `"CONFIGURE_STOP_PLOT"`
				}
				if target == "" {
					target = `"CONFIGURE_TARGET_PLOT"`
				}
				q := func(v string) string { b, _ := json.Marshal(v); return string(b) }
				return fmt.Sprintf("{\n  \"delivery_token\": %s,\n  \"signal\": {\n    \"id\": %s,\n    \"strategy\": %s,\n    \"symbol\": %s,\n    \"direction\": %s,\n    \"entry\": %s,\n    \"stop_loss\": %s,\n    \"take_profit\": %s,\n    \"timestamp\": \"{{timenow}}\"\n  }\n}", q(r.Token), q(r.ID+"-"+symbol+"-"+strings.ToLower(direction)+"-{{time}}"), q(r.Strategy), q(symbol), q(direction), entry, stop, target)
			}
			url := urls[r.ID]
			ready := func(p ingress.PriceMapping) bool { return p.Entry != "" && p.Stop != "" && p.Target != "" }
			buyReady, sellReady := ready(r.Mapping("BUY")), ready(r.Mapping("SELL"))
			out = append(out, Template{ProfileID: r.ID, Symbol: symbol, URL: url, Buy: build("BUY"), Sell: build("SELL"), Ready: buyReady && sellReady, BuyReady: buyReady, SellReady: sellReady})
		}
	}
	return out
}

// ApplyIngressPreset configures notification ingress only. It never modifies
// profile tokens, mappings, account permissions, or trading mode.
func (m *Manager) ApplyIngressPreset(name string) error {
	switch name {
	case "cloudflare-quick":
		return m.SaveIngress(true, true, "")
	case "disabled":
		return m.SaveIngress(false, false, "")
	default:
		return errors.New("unknown webhook preset")
	}
}

package dashboard

import (
	"net/http"
	"strings"

	"github.com/yelaco/projectx-pilot/internal/admin"
	"github.com/yelaco/projectx-pilot/internal/projectx"
	"github.com/yelaco/projectx-pilot/internal/risk"
)

func pageInfo(path string) (name, nav, title, subtitle string) {
	switch path {
	case "/system-health":
		return "system-health", "system-health", "System health", "Current internal state, account blockers and observation freshness — without changing execution."
	case "/runners":
		return "runners", "runners", "Execution runners", "Account ownership, global controls, and signal-to-acceptance results."
	case "/operation-days":
		return "operation-days", "operation-days", "Operation days", "Plan Saturday–Friday trading weeks in America/Chicago."
	case "/execution":
		return "execution", "execution", "Practice execution", "Manual test orders on a pinned Practice account. Automatic trading is managed in its automation profile."
	case "/":
		return "overview", "overview", "System overview", "Your local connection, alert pipeline, and review workload."
	case "/trade-managers":
		return "trade-managers", "trade-managers", "Trade managers", "Reusable risk limits and single- or multi-account routing policies."
	case "/trade-managers/new":
		return "trade-manager", "trade-managers", "New trade manager", "Define risk and account routing policy. No orders are enabled."
	case "/automations":
		return "automations", "automations", "Automations", "Organize your strategies and their assigned accounts."
	case "/automations/new":
		return "profile", "automations", "New automation", "Create a draft profile. TradingView profiles get delivery credentials automatically."
	case "/deliveries":
		return "deliveries", "inbox", "Incoming alerts", "Review deliveries before assigning them to a local profile and contract."
	case "/signals":
		return "signals", "inbox", "Signal review", "Validate and review signals locally. Review never submits an order."
	case "/activity":
		return "activity", "activity", "Activity log", "Persistent operational events, delivery errors, and connection diagnostics."
	case "/accounts":
		return "accounts", "accounts", "Accounts", "Inspect discovered accounts and manage future execution eligibility."
	case "/contracts":
		return "contracts", "accounts", "Contract directory", "Available futures expiries and authoritative tick metadata."
	case "/settings":
		return "settings", "settings", "Connection settings", "Manage your ProjectX connection and local monitoring preferences."
	case "/settings/access":
		return "access", "settings", "Admin access", "Manage local dashboard logins and revoke credentials."
	case "/settings/webhooks":
		return "webhooks", "settings", "Webhook & tunnel", "Control inbox receiving and the public connection to this device."
	}
	if strings.HasPrefix(path, "/trade-managers/profiles/") && !strings.Contains(strings.TrimPrefix(path, "/trade-managers/profiles/"), "/") {
		return "trade-manager", "trade-managers", "Trade manager", "Configure the risk limits and account set used by your automations."
	}
	if strings.HasPrefix(path, "/automations/profiles/") && !strings.Contains(strings.TrimPrefix(path, "/automations/profiles/"), "/") {
		return "profile", "automations", "Automation details", "Manage one profile, its webhook credentials, and alert messages."
	}
	return "", "", "", ""
}
func actionPage(path, id string) string {
	if profileExecutionAction(path) {
		return "/automations/profiles/" + id + "?tab=execution"
	}
	if executionAction(path) {
		return "/execution"
	}
	switch path {
	case "/fleet/review-fault", "/fleet/review-stale-quote", "/fleet/review-oco", "/fleet/pause", "/fleet/resume", "/fleet/flatten", "/fleet/account-pause", "/fleet/account-flatten":
		return "/runners"
	case "/operation-days", "/news-refresh":
		return "/operation-days"
	case "/trade-manager":
		if id != "" {
			return "/trade-managers/profiles/" + id
		}
		return "/trade-managers/new"
	case "/trade-manager-delete":
		return "/trade-managers"
	case "/automation":
		if id != "" {
			return "/automations/profiles/" + id
		}
		return "/automations/new"
	case "/automation-delete":
		return "/automations"
	case "/automation-execution":
		return "/automations/profiles/" + id + "?tab=execution"
	case "/mapping":
		return "/automations/profiles/" + id + "?tab=webhook"
	case "/delivery-import", "/delivery-reject":
		return "/deliveries"
	case "/signal-import", "/signal-review":
		return "/signals"
	case "/account-permission":
		return "/accounts"
	case "/ingress":
		return "/settings/webhooks"
	case "/admin-access":
		return "/settings/access"
	case "/settings", "/attention-settings", "/trading-report-settings":
		return "/settings"
	default:
		return "/"
	}
}
func redirectAction(w http.ResponseWriter, r *http.Request, notice string) {
	target := actionPage(r.URL.Path, r.PostForm.Get("id"))
	separator := "?"
	if strings.Contains(target, "?") {
		separator = "&"
	}
	http.Redirect(w, r, target+separator+"notice="+notice, http.StatusSeeOther)
}
func selectProfile(v *view, path, tab string) bool {
	if v.Page != "profile" {
		return true
	}
	v.Tab = tab
	if v.Tab != "webhook" && v.Tab != "messages" && v.Tab != "execution" {
		v.Tab = "general"
	}
	if path == "/automations/new" {
		v.Profile = admin.Automation{Source: "tradingview-manual", Instruments: []string{"MNQ"}}
		v.Tab = "general"
		return true
	}
	id := strings.TrimPrefix(path, "/automations/profiles/")
	for _, a := range v.Policies.Automations {
		if a.ID == id {
			v.Profile = a
			v.Editing = true
			v.Title = a.Name
			break
		}
	}
	if !v.Editing {
		return false
	}
	for _, r := range v.Ingress.Routes {
		if r.ID == id {
			copy := r
			v.Route = &copy
			break
		}
	}
	if v.Route != nil {
		v.WebhookURL = v.Monitor.WebhookURLs[v.Profile.ID]
	}
	if v.Route == nil {
		v.Tab = "general"
	}
	filtered := []admin.Template{}
	for _, t := range v.Templates {
		if t.ProfileID == id {
			filtered = append(filtered, t)
		}
	}
	v.Templates = filtered
	return true
}

func noticeText(key string) string {
	return map[string]string{
		"profile-execution":    "Profile execution settings or session updated. Check readiness and current state below.",
		"fault-lock-reviewed":  "The reviewed account fault lock was acknowledged. Positions/orders and execution history remain unchanged; trading stays paused. Existing exposure still blocks new entries.",
		"stale-quote-reviewed": "Old stale-quote lock cleared. Existing positions and orders were left untouched; trading remains paused. Start remains a separate action and entries require a flat account with no working orders.",
		"oco-reviewed":         "Known OCO checker false alarm verified; local safety lock cleared. Trading remains paused. Re-arm or Start explicitly when ready.",
		"execution":            "Execution action completed. Inspect the monitor; an accepted order is not a fill.",
		"trade-manager":        "Trade manager policy updated. New entries were disarmed; re-arm to apply changes.",
		"access":               "Administrator credentials updated. If your login changed, sign in again with the new credentials.",
		"delivery":             "Delivery updated. No order was submitted.",
		"signal":               "Signal validated and queued for local review.",
		"review":               "Review saved. No order was submitted.",
		"automation":           "Automation profiles updated.",
		"permission":           "Account permission saved. Arming and local approval are still required.",
		"saved":                "Connection settings saved. A refresh is queued.",
		"refresh":              "Connection refresh queued.",
		"ingress":              "Webhook settings saved.",
	}[key]
}

func selectTradeManager(v *view, path string) bool {
	if v.Page != "trade-manager" {
		return true
	}
	v.TradeManager = risk.Defaults()
	if path != "/trade-managers/new" {
		id := strings.TrimPrefix(path, "/trade-managers/profiles/")
		found := false
		for _, p := range v.Policies.TradeManagers {
			if p.ID == id {
				v.TradeManager = p
				v.Editing = true
				v.Title = p.Name
				found = true
			}
		}
		if !found {
			return false
		}
	}
	v.AccountChoices = append([]projectx.Account(nil), v.State.Accounts...)
	for _, id := range v.TradeManager.AccountIDs {
		found := false
		for _, a := range v.AccountChoices {
			if a.ID == id {
				found = true
			}
		}
		if !found {
			v.AccountChoices = append(v.AccountChoices, projectx.Account{ID: id, Name: "Not in current snapshot"})
		}
	}
	return true
}

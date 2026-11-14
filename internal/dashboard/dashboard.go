package dashboard

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"projectxbot/internal/access"
	"projectxbot/internal/admin"
	"projectxbot/internal/app"
	"projectxbot/internal/execution"
	"projectxbot/internal/ingress"
	"projectxbot/internal/projectx"
	"projectxbot/internal/risk"
	"projectxbot/internal/session"
	"projectxbot/internal/storage"
)

//go:embed admin.html pages/*.html
var templateFiles embed.FS

//go:embed admin.js
var adminJS string

//go:embed admin.css
var stylesheet string
var page = template.Must(template.New("admin").ParseFS(templateFiles, "admin.html", "pages/*.html"))

type view struct {
	LocalSessionLock                                                         string
	OCORecoveryCandidates                                                    []execution.Intent
	ManualTicketContracts                                                    []projectx.Contract
	PerTradeOnly                                                             bool
	ProfileResults                                                           []storage.AutomationReceipt
	ProfileRunRequested, ContinuousReady                                     bool
	ContinuousReadiness, ProfileRunMessage                                   string
	ProfileExecutionConfigured, ProfileAutomationArmed                       bool
	AutomationReady                                                          bool
	AutomationReadiness                                                      string
	AutomationResults                                                        map[string]storage.AutomationReceipt
	SessionDate, SessionStart, SessionLastEntry, SessionFlatten, SessionMode string
	PracticeFloor                                                            float64
	PracticeAccountName                                                      string
	Execution                                                                execution.Status
	ExecutionConfig                                                          execution.Config
	ExecutionPayload, ExecutionProfile, ExecutionContract                    string
	Ticket                                                                   map[string]string
	TradeManager                                                             risk.Profile
	AccountChoices                                                           []projectx.Account
	Current                                                                  access.Info
	Admins                                                                   []access.Info
	Page, Nav, Title, Subtitle, Tab                                          string
	WebhookURL                                                               string
	Profile                                                                  admin.Automation
	Editing                                                                  bool
	Route                                                                    *ingress.Route
	Ingress                                                                  ingress.Settings
	Templates                                                                []admin.Template
	Monitor                                                                  admin.Monitor
	Deliveries                                                               []storage.Record
	Signals                                                                  []storage.Record
	Policies                                                                 admin.Policies
	State                                                                    app.Snapshot
	Settings                                                                 admin.Settings
	Editable                                                                 bool
	CSRF, Notice, Error                                                      string
}

func Handler(s *app.Service, token string) http.Handler { return handler(s, token, nil) }
func handler(s *app.Service, token string, m *admin.Manager) http.Handler {
	expected := sha256.Sum256([]byte(token))
	// Derive a domain-separated form token from the persisted admin secret.
	// Open tabs remain valid across restarts; password rotation invalidates them.
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write([]byte("projectxbot/local-admin/csrf/v1"))
	fallbackCSRF := hex.EncodeToString(mac.Sum(nil))
	csrfFor := func(secret string) string {
		h := hmac.New(sha256.New, []byte(secret))
		_, _ = h.Write([]byte("projectxbot/local-admin/csrf/v1"))
		return hex.EncodeToString(h.Sum(nil))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		csrf := fallbackCSRF
		var current access.Identity
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'")
		w.Header().Set("Referrer-Policy", "same-origin")
		render := func(w http.ResponseWriter, status int, notice, message string) {
			path, tab := r.URL.Path, r.URL.Query().Get("tab")
			if r.Method == http.MethodPost {
				target := actionPage(r.URL.Path, r.PostForm.Get("id"))
				u, _ := url.Parse(target)
				path = u.Path
				tab = u.Query().Get("tab")
				if strings.HasPrefix(path, "/automations/") && status >= 400 {
					found := false
					if m != nil {
						for _, a := range m.Policies().Automations {
							if a.ID == r.PostForm.Get("id") {
								found = true
							}
						}
					}
					if !found {
						path = "/automations/new"
					}
				}
			}
			if r.Method == http.MethodPost && status >= 400 && strings.HasPrefix(path, "/trade-managers/profiles/") {
				found := false
				for _, p := range m.Policies().TradeManagers {
					if p.ID == r.PostForm.Get("id") {
						found = true
					}
				}
				if !found {
					path = "/trade-managers/new"
				}
			}
			name, nav, title, subtitle := pageInfo(path)
			if name == "" || (m == nil && name != "overview") {
				http.NotFound(w, r)
				return
			}
			if notice == "" && r.Method == http.MethodGet {
				notice = noticeText(r.URL.Query().Get("notice"))
			}
			v := view{Current: current.Info, Page: name, Nav: nav, Title: title, Subtitle: subtitle, State: s.Snapshot(), Editable: m != nil, CSRF: csrf, Notice: notice, Error: message}
			if m != nil {
				v.Settings = m.Settings()
				v.ExecutionConfig = m.ExecutionConfig()
				lockSession, lockReason, lockErr := m.ExecutionLockInfo()
				v.LocalSessionLock = lockReason
				if lockErr != nil {
					v.Error = "Cannot read bot session lock; execution stays blocked."
				}
				v.Policies = m.Policies()
				for _, contract := range v.State.Contracts {
					if !contract.ActiveContract {
						continue
					}
					for _, allowed := range v.Settings.Config.AllowedInstruments {
						if strings.HasSuffix(contract.SymbolID, "."+allowed) {
							v.ManualTicketContracts = append(v.ManualTicketContracts, contract)
							break
						}
					}
				}

				for _, manager := range v.Policies.TradeManagers {
					if manager.ID == v.ExecutionConfig.TradeManagerID {
						v.PerTradeOnly = manager.PerTradeOnly
					}
				}
				v.PracticeFloor = v.ExecutionConfig.LastVerifiedFloor
				if (r.URL.Path == "/execution/arm" || r.URL.Path == "/automation-arm") && r.Method == http.MethodPost {
					if value, err := strconv.ParseFloat(r.PostForm.Get("floor"), 64); err == nil {
						v.PracticeFloor = value
					}
				}
				v.SessionDate = lockSession
				v.SessionMode = "normal"
				normal, _ := session.NormalWindow(v.SessionDate)
				v.SessionStart = normal.Start.Format("2006-01-02T15:04")
				v.SessionLastEntry = normal.LastEntry.Format("2006-01-02T15:04")
				v.SessionFlatten = normal.Flatten.Format("2006-01-02T15:04")
				for _, account := range v.State.Accounts {
					if account.ID == v.ExecutionConfig.AccountID {
						v.PracticeAccountName = account.Name
					}
				}
				if (r.URL.Path == "/execution/arm" || r.URL.Path == "/automation-arm") && r.Method == http.MethodPost {
					v.SessionMode = r.PostForm.Get("session_mode")
					v.SessionDate = r.PostForm.Get("session_date")
					if v.SessionMode == "custom" {
						v.SessionStart = r.PostForm.Get("start")
						v.SessionLastEntry = r.PostForm.Get("last_entry")
						v.SessionFlatten = r.PostForm.Get("flatten")
					}
				}
				v.Execution = m.ExecutionStatus()
				for _, intent := range v.Execution.Intents {
					if intent.AccountID == v.ExecutionConfig.AccountID && intent.State == "closed" && intent.HaltKind == "fault" && intent.EmergencyAttempted && intent.Plan.Quantity == 1 && intent.Session == lockSession && lockReason == "protective orders missing, pending or inconsistent" {
						v.OCORecoveryCandidates = append(v.OCORecoveryCandidates, intent)
					}
				}
				v.AutomationReady, v.AutomationReadiness = m.AutomationReadiness()
				if name == "deliveries" || name == "execution" || name == "profile" {
					v.AutomationResults = map[string]storage.AutomationReceipt{}
					if results, err := m.AutomationResults(); err == nil {
						for _, result := range results {
							v.AutomationResults[result.ID] = result
							if name == "profile" {
								v.ProfileResults = append(v.ProfileResults, result)
							}
						}
					}
				}
				if r.Method == http.MethodPost && (r.URL.Path == "/execution/preview" || r.URL.Path == "/execution/ticket") {
					v.Ticket = map[string]string{}
					for _, key := range []string{"quantity", "direction", "limit_price", "stop_ticks", "target_ticks"} {
						v.Ticket[key] = r.PostForm.Get(key)
					}
					v.ExecutionPayload = r.PostForm.Get("payload")
					v.ExecutionProfile = r.PostForm.Get("profile_id")
					v.ExecutionContract = r.PostForm.Get("contract_id")
				}
				if name == "access" {
					v.Admins = m.Access().List()
				}
				if name == "profile" {
					v.Ingress = m.Ingress()
					v.Templates = m.Templates()
				} else if name == "webhooks" {
					v.Ingress = m.Ingress()
					v.Ingress.Routes = nil
				}
				v.Monitor = m.Monitor(r.Context())
				if v.Monitor.Error != "" && v.Error == "" {
					v.Error = v.Monitor.Error
				}
				if name == "deliveries" {
					records, err := m.Deliveries()
					if err != nil {
						v.Error = "Delivery inbox unavailable"
					} else {
						v.Deliveries = records
					}
				}
				if name == "signals" {
					records, err := m.Signals()
					if err != nil {
						v.Error = "Signal inbox unavailable"
					} else {
						v.Signals = records
					}
				}
			}
			if !selectProfile(&v, path, tab) || !selectTradeManager(&v, path) {
				http.NotFound(w, r)
				return
			}
			if v.Page == "profile" && r.Method == http.MethodPost && r.URL.Path == "/automation-execution" && status >= 400 {
				v.Profile.ExecutionMode = r.PostForm.Get("execution_mode")
				v.Profile.ContractID = r.PostForm.Get("contract_id")
				v.Profile.OrderType = r.PostForm.Get("order_type")
				if values, ok := r.PostForm["trade_manager_id"]; ok && len(values) == 1 {
					v.Profile.TradeManagerID = values[0]
				}
			}
			if v.Page == "profile" && v.Tab == "execution" {
				for _, manager := range v.Policies.TradeManagers {
					if manager.ID == v.Profile.TradeManagerID {
						v.PerTradeOnly = manager.PerTradeOnly
					}
				}
				v.ProfileExecutionConfigured = v.ExecutionConfig.AutomationProfileID == v.Profile.ID && v.ExecutionConfig.AccountID == v.Profile.AccountID && v.ExecutionConfig.TradeManagerID == v.Profile.TradeManagerID
				v.ProfileAutomationArmed = v.Execution.Armed && v.Execution.Automated && v.Execution.AutomationProfileID == v.Profile.ID
				v.ProfileRunRequested = v.Execution.RunRequested && v.Execution.RunProfileID == v.Profile.ID
				v.ContinuousReady, v.ContinuousReadiness = m.ContinuousReadiness(v.Profile.ID)
				if v.ProfileRunRequested {
					v.ProfileRunMessage = v.Execution.RunMessage
				} else if v.ProfileAutomationArmed {
					v.ProfileRunMessage = "Session active"
				} else if v.Execution.RunRequested && v.Execution.RunProfileID != v.Profile.ID {
					v.ProfileRunMessage = "Blocked · automation " + v.Execution.RunProfileID + " is running"
				} else if v.Execution.Armed && v.Execution.Automated {
					v.ProfileRunMessage = "Blocked · automation " + v.Execution.AutomationProfileID + " is armed"
				} else if v.Execution.Armed {
					v.ProfileRunMessage = "Paused · manual Practice session armed"
				} else if v.Execution.RunProfileID == v.Profile.ID && v.Execution.RunMessage != "" && v.Execution.RunMessage != "Paused" {
					v.ProfileRunMessage = "Paused · " + v.Execution.RunMessage
				} else {
					v.ProfileRunMessage = "Paused"
				}
				if !v.ProfileExecutionConfigured {
					v.AutomationReady = false
					v.AutomationReadiness = "Save trading setup for this profile, then use Start automation."
					v.PracticeFloor = 0
				}
				v.PracticeAccountName = ""
				for _, a := range v.State.Accounts {
					if a.ID == v.Profile.AccountID {
						v.PracticeAccountName = a.Name
					}
				}
				scoped := v.Execution.Intents[:0:0]
				for _, i := range v.Execution.Intents {
					if i.ProfileID == v.Profile.ID {
						scoped = append(scoped, i)
					}
				}
				v.Execution.Intents = scoped
				selected := v.ProfileResults[:0:0]
				for _, result := range v.ProfileResults {
					if result.ProfileID == v.Profile.ID && len(selected) < 10 {
						selected = append(selected, result)
					}
				}
				v.ProfileResults = selected
				for id, result := range v.AutomationResults {
					if result.ProfileID != v.Profile.ID {
						delete(v.AutomationResults, id)
					}
				}
			}
			var output bytes.Buffer
			if err := page.ExecuteTemplate(&output, "admin.html", v); err != nil {
				if m != nil {
					m.Note("ERROR", "dashboard", "Dashboard rendering failed.")
				}
				http.Error(w, "Dashboard unavailable", 500)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(status)
			_, _ = w.Write(output.Bytes())
		}
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", 405)
			return
		}
		if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
			if !s.Snapshot().Connected {
				http.Error(w, "unavailable", 503)
				return
			}
			_, _ = w.Write([]byte("ok\n"))
			return
		}
		user, password, ok := r.BasicAuth()
		actual := sha256.Sum256([]byte(password))
		valid := ok && user == "local" && subtle.ConstantTimeCompare(actual[:], expected[:]) == 1
		if m != nil && ok {
			current, valid = m.Access().Verify(user, password)
			if valid {
				csrf = csrfFor(current.Secret)
			}
		}
		if !valid {
			w.Header().Set("WWW-Authenticate", `Basic realm="local bot"`)
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.Method == http.MethodPost {
			if m == nil || (r.URL.Path != "/settings" && r.URL.Path != "/refresh" && r.URL.Path != "/account-permission" && r.URL.Path != "/automation" && r.URL.Path != "/automation-delete" && r.URL.Path != "/signal-import" && r.URL.Path != "/signal-review" && r.URL.Path != "/delivery-import" && r.URL.Path != "/delivery-reject" && r.URL.Path != "/ingress" && r.URL.Path != "/mapping" && r.URL.Path != "/admin-access" && r.URL.Path != "/trade-manager" && r.URL.Path != "/trade-manager-delete" && r.URL.Path != "/automation-execution" && !profileExecutionAction(r.URL.Path) && !executionAction(r.URL.Path)) {
				http.Error(w, "method not allowed", 405)
				return
			}
			// Browser form submissions may omit Origin or send null. Require
			// same-origin browser evidence as well as the secret form token below.
			if !sameOriginForm(r) {
				http.Error(w, "invalid origin", 403)
				return
			}
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
				http.Error(w, "cross-site request denied", 403)
				return
			}
			if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
				http.Error(w, "unsupported content type", 415)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 8192)
			if err := r.ParseForm(); err != nil {
				http.Error(w, "invalid or oversized form", 400)
				return
			}
			supplied := r.PostForm.Get("csrf")
			if subtle.ConstantTimeCompare([]byte(supplied), []byte(csrf)) != 1 {
				render(w, http.StatusForbidden, "", "This form has expired or is invalid. Nothing was saved. Review the refreshed form below and try again.")
				return
			}
			if profileExecutionAction(r.URL.Path) {
				if err := handleProfileExecution(r, m, current.ID); err != nil {
					render(w, 400, "", err.Error())
					return
				}
				m.Note("INFO", "automation", "Profile execution action completed; inspect its session and execution state.")
				redirectAction(w, r, "profile-execution")
				return
			}
			if r.URL.Path == "/automation-execution" {
				var err error
				if r.URL.Path == "/automation-execution" {
					if values, ok := r.PostForm["trade_manager_id"]; ok && len(values) == 1 {
						err = m.UpdateAutomationExecution(r.PostForm.Get("id"), r.PostForm.Get("execution_mode"), r.PostForm.Get("contract_id"), r.PostForm.Get("order_type"), values[0])
					} else {
						err = m.UpdateAutomationExecution(r.PostForm.Get("id"), r.PostForm.Get("execution_mode"), r.PostForm.Get("contract_id"), r.PostForm.Get("order_type"))
					}
				}
				if err != nil {
					render(w, 400, "", err.Error())
					return
				}
				m.Note("INFO", "automation", "Automatic execution configuration updated; new entries disarmed.")
				redirectAction(w, r, "automation")
				return
			}
			if executionAction(r.URL.Path) {
				if err := handleExecution(r, m, current.ID); err != nil {
					m.Note("WARN", "execution", err.Error())
					render(w, 400, "", err.Error())
					return
				}
				m.Note("INFO", "execution", "Local execution action completed; inspect the execution monitor for the resulting state.")
				if r.URL.Path == "/execution/review-oco-lock" {
					redirectAction(w, r, "oco-reviewed")
				} else if r.URL.Path == "/execution/preview" || r.URL.Path == "/execution/ticket" {
					render(w, 200, "Trade calculated. Review the preview before submitting.", "")
				} else {
					redirectAction(w, r, "execution")
				}
				return
			}
			if r.URL.Path == "/trade-manager" || r.URL.Path == "/trade-manager-delete" {
				var err error
				if r.URL.Path == "/trade-manager-delete" {
					err = m.DeleteTradeManager(r.PostForm.Get("id"))
				} else {
					var policy risk.Profile
					policy, err = parseTradeManager(r)
					if err == nil {
						err = m.SaveTradeManager(policy)
					}
				}
				if err != nil {
					render(w, 400, "", err.Error())
					return
				}
				m.Note("INFO", "risk", "Trade manager policy saved; new entries disarmed.")
				redirectAction(w, r, "trade-manager")
				return
			}
			if r.URL.Path == "/admin-access" {
				if r.PostForm.Get("new_password") != r.PostForm.Get("confirm_password") {
					render(w, 400, "", "New password and confirmation do not match.")
					return
				}
				err := m.ChangeAccess(current.ID, r.PostForm.Get("current_password"), r.PostForm.Get("action"), r.PostForm.Get("target_id"), r.PostForm.Get("username"), r.PostForm.Get("new_password"))
				if err != nil {
					render(w, 400, "", err.Error())
					return
				}
				redirectAction(w, r, "access")
				return
			}
			actions := map[string]string{"/settings": "Settings update requested.", "/automation": "Automation profile save requested.", "/automation-delete": "Automation deletion requested.", "/mapping": "Alert mapping or token rotation requested.", "/ingress": "Receiver and tunnel configuration requested.", "/signal-import": "Local signal import requested.", "/signal-review": "Signal review requested.", "/delivery-import": "Webhook delivery assignment requested.", "/delivery-reject": "Webhook delivery discard requested.", "/account-permission": "Account permission change requested.", "/refresh": "Immediate snapshot refresh requested."}
			m.Note("INFO", "admin", actions[r.URL.Path])
			if r.URL.Path == "/ingress" || r.URL.Path == "/mapping" {
				var err error
				if r.URL.Path == "/ingress" {
					if preset := r.PostForm.Get("preset"); preset != "" {
						err = m.ApplyIngressPreset(preset)
					} else {
						err = m.SaveIngress(r.PostForm.Get("enabled") == "yes", r.PostForm.Get("quick") == "yes", r.PostForm.Get("public_url"))
					}
				} else {
					mode := r.PostForm.Get("mapping_mode")
					if mode == "" {
						mode = "shared"
					}
					readMapping := func(prefix string) ingress.PriceMapping {
						return ingress.PriceMapping{Entry: r.PostForm.Get(prefix + "entry"), Stop: r.PostForm.Get(prefix + "stop"), Target: r.PostForm.Get(prefix + "target")}
					}
					err = m.SaveMappings(r.PostForm.Get("id"), mode, readMapping(""), readMapping("buy_"), readMapping("sell_"), r.PostForm.Get("rotate") == "yes")
				}
				if err != nil {
					m.Note("WARN", "admin", err.Error())
					render(w, 400, "", err.Error())
					return
				}
				m.Note("INFO", "admin", "Webhook configuration saved; new entries disarmed.")
				redirectAction(w, r, "ingress")
				return
			}
			if r.URL.Path == "/delivery-import" || r.URL.Path == "/delivery-reject" {
				var err error
				if r.URL.Path == "/delivery-import" {
					err = m.ImportDelivery(r.PostForm.Get("id"), r.PostForm.Get("profile_id"), r.PostForm.Get("contract_id"))
				} else {
					err = m.RejectDelivery(r.PostForm.Get("id"))
				}
				if err != nil {
					render(w, 400, "", err.Error())
					return
				}
				redirectAction(w, r, "delivery")
				return
			}
			if r.URL.Path == "/signal-import" {
				render(w, http.StatusGone, "", "Manual signal import is retired. Send signals through the profile webhook; use Practice execution for local API tests.")
				return
			}

			if r.URL.Path == "/signal-review" {
				if err := m.ReviewSignal(r.PostForm.Get("id"), r.PostForm.Get("status")); err != nil {
					render(w, 400, "", err.Error())
					return
				}
				redirectAction(w, r, "review")
				return
			}
			if r.URL.Path == "/refresh" {
				s.RequestRefresh()
				redirectAction(w, r, "refresh")
				return
			}
			if r.URL.Path == "/automation-delete" {
				err := m.DeleteAutomation(r.PostForm.Get("id"))
				if err != nil {
					render(w, 400, "", err.Error())
					return
				}
				redirectAction(w, r, "automation")
				return
			}
			if r.URL.Path == "/account-permission" {
				id, err := strconv.ParseInt(r.PostForm.Get("account_id"), 10, 64)
				if err != nil {
					render(w, 400, "", "Invalid account ID")
					return
				}
				if err = m.SetAccountPermission(id, r.PostForm.Get("allowed") == "yes"); err != nil {
					render(w, 400, "", err.Error())
					return
				}
				redirectAction(w, r, "permission")
				return
			}
			if r.URL.Path == "/automation" {
				if source := r.PostForm.Get("source"); source != "" && source != "tradingview-manual" {
					render(w, 400, "", "Automation profiles accept webhook signals only.")
					return
				}

				id, err := strconv.ParseInt(r.PostForm.Get("account_id"), 10, 64)
				if err != nil && r.PostForm.Get("trade_manager_id") == "" {
					render(w, 400, "", "Invalid account ID")
					return
				}
				symbols := strings.FieldsFunc(strings.ToUpper(r.PostForm.Get("instruments")), func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
				updated := admin.Automation{TradeManagerID: r.PostForm.Get("trade_manager_id"), ID: r.PostForm.Get("id"), Name: r.PostForm.Get("name"), Strategy: r.PostForm.Get("strategy"), AccountID: id, Instruments: symbols, Source: "tradingview-manual"}
				for _, old := range m.Policies().Automations {
					if old.ID == updated.ID {
						updated.ExecutionMode = old.ExecutionMode
						updated.ContractID = old.ContractID
						updated.OrderType = old.OrderType
					}
				}
				err = m.SaveAutomation(updated)
				if err != nil {
					render(w, 400, "", err.Error())
					return
				}
				redirectAction(w, r, "automation")
				return
			}
			account, err := strconv.ParseInt(r.PostForm.Get("account_id"), 10, 64)
			if err != nil {
				render(w, 400, "", "Invalid account ID")
				return
			}
			seconds, err := strconv.Atoi(r.PostForm.Get("refresh_seconds"))
			if err != nil {
				render(w, 400, "", "Invalid refresh interval")
				return
			}
			instruments := strings.FieldsFunc(strings.ToUpper(r.PostForm.Get("instruments")), func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
			err = m.Save(admin.Update{NewsFilter: r.PostForm.Get("news_filter"), AccountID: account, Instruments: instruments, RefreshSeconds: seconds, Username: r.PostForm.Get("username"), APIKey: r.PostForm.Get("api_key"), ClearCredentials: r.PostForm.Get("clear_credentials") == "yes"})
			if err != nil {
				render(w, 400, "", err.Error())
				return
			}
			redirectAction(w, r, "saved")
			return
		}
		switch r.URL.Path {
		case "/execution", "/", "/automations", "/automations/new", "/deliveries", "/signals", "/accounts", "/contracts", "/activity", "/settings", "/settings/webhooks", "/settings/access", "/trade-managers", "/trade-managers/new":
			render(w, 200, "", "")
		case "/api/monitor":
			if m == nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(m.Monitor(r.Context()))
		case "/admin.js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = w.Write([]byte(adminJS))
		case "/admin.css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
			_, _ = w.Write([]byte(stylesheet))
		case "/api/status":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(s.Snapshot())
		default:
			if strings.HasPrefix(r.URL.Path, "/automations/") || strings.HasPrefix(r.URL.Path, "/trade-managers/profiles/") {
				render(w, 200, "", "")
			} else {
				http.NotFound(w, r)
			}
		}
	})
}
func HandlerForAddress(s *app.Service, token, address string) http.Handler {
	return withHost(handler(s, token, nil), address)
}
func AdminHandler(s *app.Service, token, address string, m *admin.Manager) http.Handler {
	return withHost(handler(s, token, m), address)
}
func withHost(h http.Handler, address string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != address {
			http.Error(w, "invalid host", 403)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// sameOriginForm accepts an exact Origin, or browser evidence for forms whose
// Origin is absent/opaque. A foreign explicit Origin is never accepted.
func sameOriginForm(r *http.Request) bool {
	expected := "http://" + r.Host
	origin := r.Header.Get("Origin")
	if origin != "" && origin != "null" {
		return origin == expected
	}
	if r.Header.Get("Sec-Fetch-Site") == "same-origin" {
		return true
	}
	ref, err := url.Parse(r.Header.Get("Referer"))
	return err == nil && ref.Scheme == "http" && ref.Host == r.Host && ref.User == nil
}

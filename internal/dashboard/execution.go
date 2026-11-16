package dashboard

import (
	"errors"
	"net/http"
	"strconv"

	"projectxbot/internal/admin"
	"projectxbot/internal/execution"
	"projectxbot/internal/session"
)

func executionAction(path string) bool {
	switch path {
	case "/execution/review-oco-lock", "/execution/ticket", "/execution/settings", "/execution/arm", "/execution/pause", "/execution/preview", "/execution/submit", "/execution/flatten":
		return true
	}
	return false
}
func handleExecution(r *http.Request, m *admin.Manager, actor string) error {
	password := r.PostForm.Get("current_password")
	switch r.URL.Path {
	case "/execution/review-oco-lock":
		if r.PostForm.Get("review_confirmed") != "yes" {
			return errors.New("confirm review of the known OCO checker false alarm")
		}
		return m.ReviewOneWayOCOLock(r.Context(), actor, password, r.PostForm.Get("intent_id"))
	case "/execution/ticket":
		quantity, err := strconv.Atoi(r.PostForm.Get("quantity"))
		if err != nil {
			return errors.New("invalid contract quantity")
		}
		stop, err := strconv.Atoi(r.PostForm.Get("stop_ticks"))
		if err != nil {
			return errors.New("invalid stop ticks")
		}
		target, err := strconv.Atoi(r.PostForm.Get("target_ticks"))
		if err != nil {
			return errors.New("invalid target ticks")
		}
		orderType := 1
		entry := 0.0
		if r.PostForm.Get("order_type") == "market" {
			orderType = 2
		} else if r.PostForm.Get("order_type") == "limit" {
			entry, err = strconv.ParseFloat(r.PostForm.Get("limit_price"), 64)
			if err != nil {
				return errors.New("limit price is required for a limit order")
			}
		} else {
			return errors.New("choose Market Order or Limit Order")
		}
		return m.PreviewTicket(r.Context(), r.PostForm.Get("contract_id"), r.PostForm.Get("direction"), orderType, quantity, stop, target, entry)
	case "/execution/pause":
		m.PauseExecution()
		return nil
	case "/execution/preview":
		return errors.New("JSON execution import is retired; use the Practice API test ticket")
	case "/execution/submit":
		return m.SubmitExecution(r.Context(), actor, password, r.PostForm.Get("preview_token"))
	case "/execution/flatten":
		return m.FlattenExecution(r.Context(), actor, password)
	case "/execution/settings":
		c, err := executionSettingsForm(r)
		if err != nil {
			return err
		}
		return m.SaveExecution(actor, password, c)
	case "/execution/arm":
		auth, err := executionAuthorizationForm(r)
		if err != nil {
			return err
		}
		// Practice execution is exclusively a manual test entry point.
		auth.AutomationConfirmed = false
		return m.ArmExecution(r.Context(), actor, password, auth)
	}
	return errors.New("unknown execution action")
}

func executionWindow(r *http.Request) (session.Window, error) {
	var w session.Window
	mode := r.PostForm.Get("session_mode")
	if mode == "normal" {
		var err error
		w, err = session.NormalWindow(r.PostForm.Get("session_date"))
		if err != nil {
			return w, err
		}
	} else if mode == "custom" || mode == "" {
		var err error
		w.Start, err = session.ParseChicago(r.PostForm.Get("start"))
		if err != nil {
			return w, errors.New("choose the start time in Chicago")
		}
		w.LastEntry, err = session.ParseChicago(r.PostForm.Get("last_entry"))
		if err != nil {
			return w, errors.New("choose the last-entry time in Chicago")
		}
		w.Flatten, err = session.ParseChicago(r.PostForm.Get("flatten"))
		if err != nil {
			return w, errors.New("choose the flatten time in Chicago")
		}
	} else {
		return w, errors.New("choose normal or custom session hours")
	}
	w.Verified = r.PostForm.Get("hours_confirmed") == "yes"
	return w, nil
}

func executionSettingsForm(r *http.Request) (execution.Config, error) {
	c := execution.Config{DisablePriceDeviation: r.PostForm.Get("deviation_disabled") == "yes", Enabled: r.PostForm.Get("enabled") == "yes", TradeManagerID: r.PostForm.Get("trade_manager_id")}
	var err error
	c.AccountID, err = strconv.ParseInt(r.PostForm.Get("account_id"), 10, 64)
	if err != nil {
		return c, errors.New("invalid account ID")
	}
	c.RoundTripCosts, err = strconv.ParseFloat(r.PostForm.Get("costs"), 64)
	if err != nil {
		return c, errors.New("invalid round-trip costs")
	}
	c.SlippageTicks, err = strconv.Atoi(r.PostForm.Get("slippage"))
	if err != nil {
		return c, errors.New("invalid slippage ticks")
	}
	c.MaxQuantity, err = strconv.Atoi(r.PostForm.Get("quantity"))
	if err != nil {
		return c, errors.New("invalid maximum quantity")
	}
	c.MaxDeviationTicks, err = strconv.Atoi(r.PostForm.Get("deviation"))
	if err != nil {
		return c, errors.New("invalid deviation ticks")
	}
	return c, nil
}
func executionAuthorizationForm(r *http.Request) (execution.Authorization, error) {
	w, err := executionWindow(r)
	if err != nil {
		return execution.Authorization{}, err
	}
	floor, err := optionalFloor(r.PostForm.Get("floor"))
	if err != nil {
		return execution.Authorization{}, errors.New("invalid verified loss floor")
	}
	return execution.Authorization{Window: w, Floor: floor, AccountName: r.PostForm.Get("account_name"), PracticeConfirmed: r.PostForm.Get("practice_confirmed") == "yes", BracketsConfirmed: r.PostForm.Get("brackets_confirmed") == "yes", FloorConfirmed: r.PostForm.Get("floor_confirmed") == "yes"}, nil
}
func profileExecutionAction(path string) bool {
	switch path {
	case "/automation-setup", "/automation-start", "/automation-settings", "/automation-arm", "/automation-pause", "/automation-flatten":
		return true
	}
	return false
}
func handleProfileExecution(r *http.Request, m *admin.Manager, actor string) error {
	id, password := r.PostForm.Get("id"), r.PostForm.Get("current_password")
	switch r.URL.Path {
	case "/automation-start":
		return m.StartProfile(r.Context(), id)
	case "/automation-setup":
		c, err := executionSettingsForm(r)
		if err != nil {
			return err
		}
		floor, err := optionalFloor(r.PostForm.Get("floor"))
		if err != nil {
			return errors.New("enter the verified fixed account loss floor")
		}
		yes := r.PostForm.Get("setup_confirmed") == "yes"
		return m.SaveProfileSetup(actor, password, id, c, c.Enabled, execution.Authorization{Floor: floor, Window: session.Window{Verified: yes}, PracticeConfirmed: yes, BracketsConfirmed: yes, FloorConfirmed: yes})
	case "/automation-settings":
		return errors.New("legacy automatic settings are retired; use this profile’s trading setup")
	case "/automation-arm":
		a, err := executionAuthorizationForm(r)
		if err != nil {
			return err
		}
		_ = a
		return errors.New("legacy automatic arming is retired; save trading setup and use Start on the profile")
	case "/automation-pause":
		return m.PauseProfile(id)
	case "/automation-flatten":
		return m.FlattenProfile(r.Context(), actor, password, id)
	}
	return errors.New("unknown profile execution action")
}

func optionalFloor(value string) (float64, error) {
	if value == "" {
		return 0, nil
	}
	return strconv.ParseFloat(value, 64)
}

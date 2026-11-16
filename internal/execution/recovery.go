package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yelaco/projectx-pilot/internal/projectx"
	"github.com/yelaco/projectx-pilot/internal/session"
)

const legacyOCOLock = "protective orders missing, pending or inconsistent"

// ReviewOneWayOCOLock repairs only the old reciprocal-link check's false alarm.
// The caller must authenticate the local operator and bind the account. Every
// remote call below is read-only; resolution never arms or submits anything.
func (e *Engine) ReviewOneWayOCOLock(ctx context.Context, account int64, intentID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.publishLocked()
	if e.ctx == nil || e.ctx.Err() != nil {
		return errors.New("execution monitor must be running for local lock review")
	}
	if e.active() != nil || e.runRequested || e.now().Before(e.armedUntil) {
		return errors.New("pause execution and finish reconciliation before local lock review")
	}
	var candidate *Intent
	for n := range e.intents {
		if e.intents[n].ID == intentID && e.intents[n].AccountID == account {
			candidate = &e.intents[n]
			break
		}
	}
	if candidate == nil || candidate.State != "closed" || candidate.HaltKind != "fault" || !candidate.EmergencyAttempted || candidate.Plan.Quantity != 1 || candidate.Session != session.CurrentTradingDate(e.now()) {
		return errors.New("select this session's closed one-contract OCO false-alarm execution; unresolved or unrelated executions cannot be cleared")
	}
	why, err := e.store.ExecutionLock(strconv.FormatInt(account, 10), candidate.Session)
	if err != nil {
		return err
	}
	if why != legacyOCOLock {
		return errors.New("this action only resolves the old OCO checker lock; loss limits, uncertain orders and other faults are unchanged")
	}
	resolver, ok := e.store.(interface {
		ResolveOneWayOCOLock(string, string, string, string) error
	})
	if !ok {
		return errors.New("store does not support audited OCO lock review")
	}
	if err = e.connect(account); err != nil {
		return err
	}
	history, err := e.api.Orders(ctx, account, candidate.Window.StartOfSession(), e.now().UTC())
	if err != nil {
		return errors.New("historical orders unavailable; local lock retained")
	}
	if err = verifyHistoricalOneWay(*candidate, history); err != nil {
		return err
	}
	flat := func() error {
		a, err := e.account(ctx, account)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(strings.ToUpper(a.Name), "PRAC") {
			return errors.New("local review supports verified PRAC accounts only")
		}
		positions, err := e.api.OpenPositions(ctx, account)
		if err != nil {
			return errors.New("fresh positions unavailable; local lock retained")
		}
		orders, err := e.api.OpenOrders(ctx, account)
		if err != nil {
			return errors.New("fresh orders unavailable; local lock retained")
		}
		if len(positions) != 0 || len(orders) != 0 {
			return errors.New("account is not flat with zero working orders; local lock retained")
		}
		return nil
	}
	if err = flat(); err != nil {
		return err
	}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	if err = flat(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if candidate.Session != session.CurrentTradingDate(e.now()) {
		return errors.New("trading session changed during review; local lock retained")
	}
	if e.ctx.Err() != nil {
		return errors.New("monitor stopped; local lock retained")
	}
	if err = resolver.ResolveOneWayOCOLock(strconv.FormatInt(account, 10), candidate.Session, candidate.ID, legacyOCOLock); err != nil {
		return err
	}
	e.disarm()
	e.runRequested = false
	e.runMessage = "Paused"
	e.message = ""
	if e.audit != nil {
		e.audit("WARN", "Known one-way OCO checker lock reviewed and cleared after historical verification and two fresh flat-account observations. Execution remains paused.")
	}
	return nil
}

const staleQuoteLock = "market data stale while managing exposure"

// ReviewStaleQuoteLock resolves only the retired stale-quote emergency trigger.
// It performs read-only broker checks and never arms, replays or clears risk locks.
func (e *Engine) ReviewStaleQuoteLock(ctx context.Context, account int64, intentID string, confirmation ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.publishLocked()
	if e.ctx == nil || e.ctx.Err() != nil {
		return errors.New("execution monitor must be running for local lock review")
	}
	if e.active() != nil || e.runRequested || e.now().Before(e.armedUntil) {
		return errors.New("pause execution and finish reconciliation before local lock review")
	}
	var candidate *Intent
	for n := range e.intents {
		if e.intents[n].ID == intentID && e.intents[n].AccountID == account {
			candidate = &e.intents[n]
			break
		}
	}
	if candidate == nil || candidate.State != "closed" || candidate.HaltKind != "fault" || !candidate.EmergencyAttempted || candidate.Session != session.CurrentTradingDate(e.now()) {
		return errors.New("select this session's closed stale-quote execution; unresolved or unrelated executions cannot be cleared")
	}
	why, err := e.store.ExecutionLock(strconv.FormatInt(account, 10), candidate.Session)
	if err != nil {
		return err
	}
	if why != staleQuoteLock {
		return errors.New("this action only resolves the stale-quote lock; loss limits, uncertain orders and other faults are unchanged")
	}
	resolver, ok := e.store.(interface {
		ResolveStaleQuoteLock(string, string, string, string) error
	})
	if !ok {
		return errors.New("store does not support audited stale-quote lock review")
	}
	if err = e.connect(account); err != nil {
		return err
	}
	history, err := e.api.Orders(ctx, account, candidate.Window.StartOfSession(), e.now().UTC())
	if err != nil {
		return errors.New("historical orders unavailable; local lock retained")
	}
	if err = verifyStaleQuoteHistory(*candidate, history); err != nil {
		return err
	}
	observe := func() ([]projectx.Position, []projectx.Order, error) {
		a, err := e.account(ctx, account)
		if err != nil {
			return nil, nil, err
		}
		if a.ID != account {
			return nil, nil, errors.New("account identity mismatch")
		}
		positions, err := e.api.OpenPositions(ctx, account)
		if err != nil {
			return nil, nil, errors.New("fresh positions unavailable; local lock retained")
		}
		orders, err := e.api.OpenOrders(ctx, account)
		if err != nil {
			return nil, nil, errors.New("fresh orders unavailable; local lock retained")
		}
		return positions, orders, nil
	}
	positions, orders, err := observe()
	if err != nil {
		return err
	}
	first := reviewSnapshot(positions, orders)
	open := len(positions) > 0 || len(orders) > 0
	confirmed := false
	token := ""
	if len(confirmation) == 1 {
		token = confirmation[0]
	}
	if open {
		challenge := e.staleQuoteReview
		if token == "" || challenge == nil || challenge.Token != token || challenge.AccountID != account || challenge.IntentID != candidate.ID || challenge.Session != candidate.Session || !e.now().Before(challenge.Expires) || challenge.Snapshot != first {
			return e.requestStaleQuoteConfirmation(*candidate, positions, orders)
		}
		confirmed = true
	} else if token != "" {
		// A confirmation is bound to exactly what was shown, even if the account became flat.
		return errors.New("account changed since confirmation; review again before clearing this lock")
	}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	latestPositions, latestOrders, err := observe()
	if err != nil {
		return err
	}
	if reviewSnapshot(latestPositions, latestOrders) != first {
		if len(latestPositions) > 0 || len(latestOrders) > 0 {
			return e.requestStaleQuoteConfirmation(*candidate, latestPositions, latestOrders)
		}
		return errors.New("account changed during review; review again before clearing this lock")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if candidate.Session != session.CurrentTradingDate(e.now()) {
		return errors.New("trading session changed during review; local lock retained")
	}
	if e.ctx.Err() != nil {
		return errors.New("monitor stopped; local lock retained")
	}
	if confirmed {
		if e.staleQuoteReview == nil || !e.now().Before(e.staleQuoteReview.Expires) {
			return e.requestStaleQuoteConfirmation(*candidate, latestPositions, latestOrders)
		}
		scoped, ok := e.store.(interface {
			ResolveConfirmedStaleQuoteLock(string, string, string, string, string, int, int) error
		})
		if !ok {
			return errors.New("store does not support audited open-exposure confirmation")
		}
		if err = scoped.ResolveConfirmedStaleQuoteLock(strconv.FormatInt(account, 10), candidate.Session, candidate.ID, staleQuoteLock, first, len(positions), len(orders)); err != nil {
			return err
		}
	} else if err = resolver.ResolveStaleQuoteLock(strconv.FormatInt(account, 10), candidate.Session, candidate.ID, staleQuoteLock); err != nil {
		return err
	}
	e.staleQuoteReview = nil
	e.disarm()
	e.runRequested = false
	e.runMessage = "Paused"
	e.message = ""
	if e.audit != nil {
		if confirmed {
			e.audit("WARN", "Stale-quote lock cleared after explicit open-exposure confirmation and two fresh matching observations. Positions/orders unchanged; execution remains paused.")
		} else {
			e.audit("WARN", "Stale-quote lock reviewed and cleared after historical verification and two fresh flat-account observations. Execution remains paused.")
		}
	}
	return nil
}

func verifyHistoricalOneWay(i Intent, history []projectx.Order) error {
	if i.OrderID <= 0 || i.Tag == "" || (i.Signal.Direction != "BUY" && i.Signal.Direction != "SELL") {
		return errors.New("original entry identity unavailable; local lock retained")
	}
	var parent *projectx.Order
	var children []projectx.Order
	seen := map[int64]bool{}
	for n := range history {
		o := history[n]
		if seen[o.ID] {
			return errors.New("duplicate historical order observations; local lock retained")
		}
		seen[o.ID] = true
		if o.ID == i.OrderID {
			copy := o
			parent = &copy
		}
		if o.ParentOrderID != nil && *o.ParentOrderID == i.OrderID {
			children = append(children, o)
		}
	}
	if parent == nil || parent.CustomTag != i.Tag || parent.AccountID != i.AccountID || parent.ContractID != i.Contract.ID || parent.Type != i.OrderType || parent.Side != side(i.Signal.Direction) || parent.Status != 2 || parent.FillVolume != 1 || parent.Size != 1 || parent.FilledPrice == nil || !finite(*parent.FilledPrice) || *parent.FilledPrice <= 0 || len(children) != 2 {
		return errors.New("complete matching entry/fill/bracket history required; local lock retained")
	}
	if !finite(i.Contract.TickSize) || i.Contract.TickSize <= 0 || i.Plan.StopTicks <= 0 || i.Plan.TargetTicks <= 0 {
		return errors.New("original protection plan unavailable; local lock retained")
	}
	links := 0
	for n := range children {
		c := &children[n]
		if c.AccountID != i.AccountID || c.Status != 3 || c.FillVolume != 0 || c.Size != 1 {
			return errors.New("repair requires cancelled, unfilled protective children; local lock retained")
		}
		if c.LinkedOrderID != nil && *c.LinkedOrderID > 0 {
			links++
		}
		// Validate historical relationships/prices using the same pure checker. This
		// normalization is solely for completed cancelled orders, not proof of live coverage.
		c.Status = 1
	}
	if links != 1 {
		return errors.New("history does not identify the known one-way OCO layout; local lock retained")
	}
	p := projectx.Position{AccountID: i.AccountID, ContractID: i.Contract.ID, Type: positionType(i.Signal.Direction), Size: 1, AveragePrice: *parent.FilledPrice}
	if err := verifyProtection(i, p, children); err != nil {
		return errors.New("historical protection fails current verification; local lock retained")
	}
	return nil
}

func verifyStaleQuoteHistory(i Intent, history []projectx.Order) error {
	if i.HaltReason != staleQuoteLock || i.Plan.Quantity < 1 || i.OrderID <= 0 || i.Tag == "" || (i.Signal.Direction != "BUY" && i.Signal.Direction != "SELL") || !finite(i.Contract.TickSize) || i.Contract.TickSize <= 0 || i.Plan.StopTicks <= 0 || i.Plan.TargetTicks <= 0 {
		return errors.New("original stale-quote trigger required; local lock retained")
	}
	var parent *projectx.Order
	for n := range history {
		if history[n].ID == i.OrderID {
			if parent != nil {
				return errors.New("duplicate entry history")
			}
			parent = &history[n]
		}
	}
	if parent == nil || parent.AccountID != i.AccountID || parent.CustomTag != i.Tag || parent.ContractID != i.Contract.ID || parent.Type != i.OrderType || parent.Side != side(i.Signal.Direction) || parent.Size != i.Plan.Quantity || parent.FillVolume < 1 || parent.FillVolume > parent.Size || (parent.Status != 2 && parent.Status != 3) || parent.FilledPrice == nil || !finite(*parent.FilledPrice) || *parent.FilledPrice <= 0 {
		return errors.New("matching terminal filled entry history required; local lock retained")
	}
	p := projectx.Position{AccountID: i.AccountID, ContractID: i.Contract.ID, Type: positionType(i.Signal.Direction), Size: parent.FillVolume, AveragePrice: *parent.FilledPrice}
	if err := verifyProtection(i, p, i.HaltOrders); err != nil {
		return errors.New("original verified protection evidence required; local lock retained")
	}
	return nil
}

// StaleQuoteConfirmationRequired is a fresh review prompt, not permission to trade.
// The token is ephemeral, short-lived and scoped to the exact account/intent/snapshot.
type StaleQuoteConfirmationRequired struct {
	Reason          string
	AccountID       int64
	IntentID, Token string
	Expires         time.Time
	Positions       []projectx.Position
	Orders          []projectx.Order
}

func (p *StaleQuoteConfirmationRequired) Error() string {
	if p.Reason != "" {
		return fmt.Sprintf("Account #%d: review %s with %d open positions and %d working orders. Confirm leaving the account untouched.", p.AccountID, p.Reason, len(p.Positions), len(p.Orders))
	}
	return fmt.Sprintf("Account #%d still has %d open position(s) and %d working order(s). Confirm you want to clear only the old stale-quote lock and leave them untouched.", p.AccountID, len(p.Positions), len(p.Orders))
}

type staleQuoteChallenge struct {
	AccountID                          int64
	IntentID, Session, Snapshot, Token string
	Expires                            time.Time
}

func reviewSnapshot(positions []projectx.Position, orders []projectx.Order) string {
	positions = append([]projectx.Position(nil), positions...)
	orders = append([]projectx.Order(nil), orders...)
	sort.Slice(positions, func(a, b int) bool { return positions[a].ID < positions[b].ID })
	sort.Slice(orders, func(a, b int) bool { return orders[a].ID < orders[b].ID })
	data, _ := json.Marshal(struct {
		Positions []projectx.Position
		Orders    []projectx.Order
	}{positions, orders})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
func (e *Engine) requestStaleQuoteConfirmation(i Intent, positions []projectx.Position, orders []projectx.Order) error {
	token := randomID()
	expiry := e.now().Add(2 * time.Minute).UTC()
	e.staleQuoteReview = &staleQuoteChallenge{AccountID: i.AccountID, IntentID: i.ID, Session: i.Session, Snapshot: reviewSnapshot(positions, orders), Token: token, Expires: expiry}
	return &StaleQuoteConfirmationRequired{AccountID: i.AccountID, IntentID: i.ID, Token: token, Expires: expiry, Positions: positions, Orders: orders}
}

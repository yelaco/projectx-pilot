package execution

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/yelaco/projectx-pilot/internal/lockreview"
	"github.com/yelaco/projectx-pilot/internal/projectx"
	"github.com/yelaco/projectx-pilot/internal/session"
	"github.com/yelaco/projectx-pilot/internal/storage"
)

type faultLockChallenge struct {
	Token, IntentID, Session, Reason, Snapshot string
	AccountID                                  int64
	Expires                                    time.Time
}

// ReviewFaultLock acknowledges only a verified, reason-scoped local fault. It
// never closes an intent, resolves an uncertain write, arms, or mutates broker state.
func (e *Engine) ReviewFaultLock(ctx context.Context, account int64, intentID, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.publishLocked()
	if e.ctx == nil || e.ctx.Err() != nil {
		return errors.New("execution monitor must be running for lock review")
	}
	if e.runRequested || e.now().Before(e.armedUntil) {
		return errors.New("pause execution before reviewing this lock")
	}
	var candidate *Intent
	for n := range e.intents {
		if e.intents[n].ID == intentID && e.intents[n].AccountID == account {
			candidate = &e.intents[n]
			break
		}
	}
	if candidate == nil || candidate.Session != session.CurrentTradingDate(e.now()) {
		return errors.New("select this session's matching execution for lock review")
	}
	reason, err := e.store.ExecutionLock(strconv.FormatInt(account, 10), candidate.Session)
	if err != nil {
		return err
	}
	policy := lockreview.Classify(reason)
	if !policy.Reviewable {
		return errors.New(policy.Guidance + " Local lock retained.")
	}
	if candidate.RiskLockReason != "" || lockreview.IsRisk(candidate.HaltReason) || candidate.State == "risk-lockout" {
		return errors.New("risk-limit evidence cannot be overridden by confirmation; local lock retained")
	}
	if candidate.State == "uncertain" || candidate.State == "submitting" {
		return errors.New("the original order is unresolved; fresh reconciliation is required before confirmation")
	}
	legacyTerminal := policy.TerminalOnly && terminal(candidate.State) && candidate.LockReason == "" && candidate.HaltReason == ""
	if candidate.LockReason != reason && candidate.HaltReason != reason && !legacyTerminal {
		return errors.New("selected execution does not identify this exact account lock")
	}
	for n := range e.intents {
		other := &e.intents[n]
		if other.AccountID == account && other.ID != candidate.ID && !terminal(other.State) {
			return errors.New("another execution is unresolved on this account; local lock retained")
		}
	}
	if (policy.TerminalOnly || candidate.EmergencyAttempted) && !terminal(candidate.State) {
		return errors.New("this order or emergency action must first reconcile to a terminal execution; confirmation cannot resolve uncertainty")
	}
	if err = e.connect(account); err != nil {
		return err
	}
	history, err := e.api.Orders(ctx, account, candidate.Window.StartOfSession(), e.now().UTC())
	if err != nil {
		return errors.New("fresh order history unavailable; local lock retained")
	}
	if err = verifyReviewParent(*candidate, history); err != nil {
		return err
	}
	observe := func() ([]projectx.Position, []projectx.Order, error) {
		if _, err := e.account(ctx, account); err != nil {
			return nil, nil, errors.New("fresh account eligibility unavailable; local lock retained")
		}
		positions, err := e.api.OpenPositions(ctx, account)
		if err != nil {
			return nil, nil, errors.New("fresh positions unavailable; local lock retained")
		}
		orders, err := e.api.OpenOrders(ctx, account)
		if err != nil {
			return nil, nil, errors.New("fresh working orders unavailable; local lock retained")
		}
		return positions, orders, nil
	}
	positions, orders, err := observe()
	if err != nil {
		return err
	}
	snapshot := reviewSnapshot(positions, orders)
	prompt := func(p []projectx.Position, o []projectx.Order) error {
		challenge := &faultLockChallenge{Token: randomID(), IntentID: candidate.ID, Session: candidate.Session, Reason: reason, Snapshot: reviewSnapshot(p, o), AccountID: account, Expires: e.now().Add(2 * time.Minute).UTC()}
		e.faultLockReview = challenge
		return &StaleQuoteConfirmationRequired{Reason: reason, AccountID: account, IntentID: candidate.ID, Token: challenge.Token, Expires: challenge.Expires, Positions: p, Orders: o}
	}
	challenge := e.faultLockReview
	if token == "" || challenge == nil || token != challenge.Token || challenge.IntentID != candidate.ID || challenge.AccountID != account || challenge.Session != candidate.Session || challenge.Reason != reason || challenge.Snapshot != snapshot || !e.now().Before(challenge.Expires) {
		return prompt(positions, orders)
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
	if snapshot != reviewSnapshot(latestPositions, latestOrders) || !e.now().Before(challenge.Expires) {
		return prompt(latestPositions, latestOrders)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if e.ctx.Err() != nil || candidate.Session != session.CurrentTradingDate(e.now()) {
		return errors.New("monitor/session changed during review; lock retained")
	}
	resolver, ok := e.store.(interface {
		ResolveFaultLock(storage.ExecRow, string, string, string) error
	})
	if !ok {
		return errors.New("store does not support atomic account fault review")
	}
	rows, err := e.store.Executions()
	if err != nil {
		return err
	}
	var before *storage.ExecRow
	for n := range rows {
		if rows[n].ID == candidate.ID && rows[n].Account == strconv.FormatInt(account, 10) {
			before = &rows[n]
			break
		}
	}
	if before == nil || before.State != candidate.State {
		return errors.New("execution changed during review; local lock retained")
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(before.Payload), &payload) != nil {
		return errors.New("execution evidence unavailable; lock retained")
	}
	at := e.now().UTC()
	payload["AcknowledgedLockActive"], _ = json.Marshal(true)
	payload["AcknowledgedLockReason"], _ = json.Marshal(reason)
	payload["AcknowledgedLockSnapshot"], _ = json.Marshal(snapshot)
	payload["AcknowledgedLockAt"], _ = json.Marshal(at)
	after, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err = resolver.ResolveFaultLock(*before, string(after), reason, snapshot); err != nil {
		return err
	}
	candidate.AcknowledgedLockActive = true
	candidate.AcknowledgedLockReason = reason
	candidate.AcknowledgedLockSnapshot = snapshot
	candidate.AcknowledgedLockAt = at
	e.faultLockReview = nil
	e.disarm()
	e.runRequested = false
	e.runMessage = "Paused; account lock reviewed"
	e.message = ""
	if e.audit != nil {
		e.audit("WARN", "Account fault lock explicitly reviewed against two matching fresh observations. Positions, orders, original trigger, claims and emergency latches unchanged; execution remains paused.")
	}
	return nil
}
func verifyReviewParent(i Intent, history []projectx.Order) error {
	if i.Plan.Quantity < 1 || i.Contract.ID == "" || (i.Signal.Direction != "BUY" && i.Signal.Direction != "SELL") {
		return errors.New("original execution identity unavailable; lock retained")
	}
	var parent *projectx.Order
	for n := range history {
		o := &history[n]
		if o.ID == i.OrderID || o.CustomTag == i.Tag {
			if parent != nil {
				return errors.New("duplicate parent identity requires reconciliation; lock retained")
			}
			parent = o
		}
	}
	if i.OrderID <= 0 || i.Tag == "" || parent == nil || parent.ID != i.OrderID || parent.CustomTag != i.Tag || parent.AccountID != i.AccountID || parent.ContractID != i.Contract.ID || parent.Size != i.Plan.Quantity || parent.Type != i.OrderType || parent.Side != side(i.Signal.Direction) {
		return errors.New("matching original order history required; local lock retained")
	}
	if parent.Status < 1 || parent.Status > 5 || parent.FillVolume < 0 || parent.FillVolume > parent.Size {
		return errors.New("invalid order observation; local lock retained")
	}
	if terminal(i.State) && parent.Status == 1 {
		return errors.New("terminal execution has a working parent; reconciliation required")
	}
	return nil
}

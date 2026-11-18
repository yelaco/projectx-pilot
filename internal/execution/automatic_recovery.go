package execution

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/yelaco/projectx-pilot/internal/lockreview"
	"github.com/yelaco/projectx-pilot/internal/session"
	"github.com/yelaco/projectx-pilot/internal/storage"
)

// ResumeAfterRecovery retains only a Start made in this process. It never places
// an order or restores authority after Pause/restart. Fleet must still freshly Arm.
func (e *Engine) ResumeAfterRecovery(ctx context.Context, profile, date string, contracts ...string) (ready bool, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.publishLocked()
	defer func() {
		if err != nil {
			e.recoveryFlatAt = time.Time{}
			e.recoveryKey = ""
		}
	}()
	if !e.runWanted || e.ctx == nil || e.ctx.Err() != nil || !e.config.Automated || e.runProfileID != profile || e.config.AutomationProfileID != profile || date != session.CurrentTradingDate(e.now()) {
		return false, errors.New("original Start permission is unavailable")
	}
	if e.active() != nil {
		return false, errors.New("execution still requires reconciliation")
	}
	account := strconv.FormatInt(e.config.AccountID, 10)
	reason, err := e.store.ExecutionLock(account, date)
	if err != nil {
		return false, err
	}
	var before *storage.ExecRow
	if reason != "" {
		if !lockreview.AutoRecoverable(reason) {
			return false, errors.New("retained lock requires review or a verified later session")
		}
		rows, readErr := e.store.Executions()
		if readErr != nil {
			return false, readErr
		}
		for _, row := range rows {
			if row.Account != account || row.Session != date || row.State != "closed" {
				continue
			}
			var i Intent
			if json.Unmarshal([]byte(row.Payload), &i) != nil {
				return false, errors.New("recovery evidence unavailable")
			}
			if i.ProfileID == profile && !i.Created.Before(e.runStartedAt) && i.LockReason == reason && lockreview.AutoRecoverable(i.HaltReason) && i.RiskLockReason == "" && !i.EmergencyAttempted && i.Contract.ID == e.config.AutomationContractID && (before == nil || i.Updated.After(rowUpdated(*before))) {
				copy := row
				before = &copy
			}
		}
		if before == nil {
			return false, errors.New("lock has no resolved execution from this Start; review required")
		}
	}
	if err = e.connect(e.config.AccountID); err != nil {
		return false, err
	}
	accountState, accountErr := e.account(ctx, e.config.AccountID)
	if accountErr != nil {
		return false, accountErr
	}
	if !e.policy.PerTradeOnly {
		window, open, windowErr := e.policy.TradingSchedule().Window(e.now())
		if windowErr != nil || !open || window.ID() != date {
			return false, errors.New("verified recovery session unavailable")
		}
		history, historyErr := e.api.Orders(ctx, e.config.AccountID, window.StartOfSession(), e.now().UTC())
		if historyErr != nil {
			return false, errors.New("fresh recovery entry history unavailable")
		}
		trades, tradeErr := e.api.Trades(ctx, e.config.AccountID, window.StartOfSession(), e.now().UTC())
		if tradeErr != nil {
			return false, errors.New("fresh recovery accounting unavailable")
		}
		loss, lossErr := sessionLoss(trades)
		if lossErr != nil {
			return false, errors.New("fresh recovery accounting unavailable")
		}
		count := 0
		for _, order := range history {
			if order.ParentOrderID == nil && order.FillVolume > 0 {
				count++
			}
		}
		reserved := 0
		for _, intent := range e.intents {
			if intent.AccountID == e.config.AccountID && intent.Session == date && intent.State != "rejected" {
				reserved++
			}
		}
		if reserved > count {
			count = reserved
		}
		if loss >= e.policy.MaxDailyLoss || count >= e.policy.MaxTradesPerSession || !finite(e.auth.Floor) || e.auth.Floor <= 0 || accountState.Balance <= e.auth.Floor {
			return false, errors.New("session risk eligibility has not recovered")
		}
	}
	if ctx.Err() != nil {
		err = ctx.Err()
		return false, err
	}
	positions, err := e.api.OpenPositions(ctx, e.config.AccountID)
	if err != nil {
		return false, errors.New("fresh recovery positions unavailable")
	}
	orders, err := e.api.OpenOrders(ctx, e.config.AccountID)
	if err != nil {
		return false, errors.New("fresh recovery orders unavailable")
	}
	if len(positions) != 0 || len(orders) != 0 {
		return false, errors.New("waiting for flat account with no working orders; account untouched")
	}
	contract := e.config.AutomationContractID
	if len(contracts) > 1 {
		return false, errors.New("select one recovery contract")
	}
	if len(contracts) == 1 && contracts[0] != contract {
		if e.config.AutomationSymbolID == "" {
			return false, errors.New("rollover is not authorized")
		}
		metadata, lookupErr := e.api.Contract(ctx, contracts[0])
		if lookupErr != nil || metadata.ID != contracts[0] || metadata.SymbolID != e.config.AutomationSymbolID || !metadata.ActiveContract {
			return false, errors.New("recovery rollover contract could not be verified")
		}
		contract = contracts[0]
	}
	e.quotes.Watch(e.ctx, contract)
	q, err := e.entryQuote(ctx, contract)
	if err != nil {
		return false, errors.New("fresh recovery market data unavailable")
	}
	now := e.now().UTC()
	if !finite(q.Price) || q.Price <= 0 || q.At.IsZero() || now.Sub(q.At) > 5*time.Second || q.At.After(now.Add(time.Second)) {
		return false, errors.New("fresh recovery market data unavailable")
	}
	key := profile + "/" + account + "/" + date + "/" + reason + "/" + contract
	if before != nil {
		key += "/" + before.ID + "/" + before.Payload
	}
	if e.recoveryKey != key || e.recoveryFlatAt.IsZero() {
		e.recoveryKey = key
		e.recoveryFlatAt = now
		return false, nil
	}
	if now.Before(e.recoveryFlatAt) {
		return false, errors.New("recovery clock moved backwards")
	}
	if now.Sub(e.recoveryFlatAt) < 3*time.Second {
		return false, nil
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if before != nil {
		resolver, ok := e.store.(interface {
			ResolveRecoveredExecutionLock(storage.ExecRow, string, time.Time, time.Time) error
		})
		if !ok {
			return false, errors.New("audited automatic recovery unavailable")
		}
		if err = resolver.ResolveRecoveredExecutionLock(*before, reason, e.recoveryFlatAt, now); err != nil {
			return false, err
		}
		if e.audit != nil {
			e.audit("INFO", "Technical execution lock recovered after closed history and two fresh flat/no-order observations. Original trigger retained; no orders replayed or changed.")
		}
	}
	e.runRequested = true
	e.message = ""
	e.runMessage = "Running · recovered; fresh arming checks required"
	e.recoveryFlatAt = time.Time{}
	e.recoveryKey = ""
	return true, nil
}
func rowUpdated(row storage.ExecRow) time.Time {
	var i struct{ Updated time.Time }
	_ = json.Unmarshal([]byte(row.Payload), &i)
	return i.Updated
}

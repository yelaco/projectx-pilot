package execution

import (
	"context"
	"errors"
	"time"

	"projectxbot/internal/projectx"
	"projectxbot/internal/signal"
)

// ExitAutomation consumes a durably claimed exit. It never creates an opposite
// entry order: the gateway close-position operation owns the market exit.
func (e *Engine) ExitAutomation(ctx context.Context, profile string, s signal.Signal, contract string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !s.IsExit() {
		return errors.New("exit action required")
	}
	if err := s.ValidateSchema(e.now(), e.config.maxAge()); err != nil {
		return err
	}
	if !e.config.Enabled || !e.config.Automated || e.paused || !e.now().Before(e.armedUntil) || e.config.AutomationProfileID != profile || e.config.AutomationContractID != contract || s.Timestamp.Before(e.armedAt) {
		return errors.New("exit does not match the locally running automation")
	}
	var i *Intent
	for n := range e.intents {
		if e.intents[n].ID == s.TradeID {
			i = &e.intents[n]
			break
		}
	}
	if i == nil || i.ProfileID != profile || i.Source != "tradingview" || i.AccountID != e.config.AccountID || i.Contract.ID != contract || i.Signal.Symbol != s.Symbol || i.Signal.Strategy != s.Strategy || i.Signal.Direction != s.Direction {
		return errors.New("exit reference does not match a bot-managed trade in this profile/account")
	}
	if s.Timestamp.Before(i.Signal.Timestamp) {
		return errors.New("exit predates referenced entry")
	}
	if terminal(i.State) || i.EmergencyAttempted {
		return nil
	}
	if i != e.active() || i.State == "uncertain" || i.State == "submitting" || i.HaltReason != "" || i.CancelAttempted {
		return errors.New("trade requires reconciliation; exit will not replay")
	}
	if err := e.connect(i.AccountID); err != nil {
		return errors.New("exit account connection unavailable")
	}
	// Confirm ownership before the one-attempt mutation latch. Unknown external
	// exposure is never flattened by an ordinary strategy exit.
	if err := e.exitScope(ctx, i); err != nil {
		return err
	}
	i.HaltKind = "signal_exit"
	i.HaltReason = "Trade exit requested by " + s.Action + " alert."
	i.HaltOrders = append([]projectx.Order(nil), i.ObservedOrders...)
	i.State = "flattening"
	i.Message = "Exit requested; cancellation and flatness verification pending."
	if err := e.persist(i); err != nil {
		return err
	}
	wireCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 12*time.Second)
	defer cancel()
	if err := e.flatten(wireCtx, i); err != nil {
		e.lock(i, "signal exit uncertain; verify TopstepX; no automatic retry")
		_ = e.persist(i)
		return errors.New("signal exit uncertain; verify TopstepX; no automatic retry")
	}
	i.Message = "Exit requests sent; waiting for flatness verification."
	if err := e.persist(i); err != nil {
		e.disarm()
		return err
	}
	select {
	case e.wake <- struct{}{}:
	default:
	}
	return nil
}

// exitScope is repeated after entry cancellation to account for a fill racing
// cancellation. This deliberately rejects unrelated orders or unexplained size.
func (e *Engine) exitScope(ctx context.Context, i *Intent) error {
	history, err := e.api.Orders(ctx, i.AccountID, i.Window.StartOfSession(), e.now().UTC())
	if err != nil {
		return errors.New("exit history unavailable")
	}
	var parent *projectx.Order
	for n := range history {
		o := &history[n]
		if o.ID == i.OrderID || o.CustomTag == i.Tag {
			if parent != nil {
				return errors.New("exit parent identity ambiguous")
			}
			parent = o
		}
	}
	if parent == nil || parent.ID != i.OrderID || parent.CustomTag != i.Tag || parent.ContractID != i.Contract.ID || parent.Side != side(i.Signal.Direction) || parent.Size != i.Plan.Quantity || parent.Type != i.OrderType {
		return errors.New("exit parent identity unavailable or inconsistent")
	}
	orders, err := e.api.OpenOrders(ctx, i.AccountID)
	if err != nil {
		return errors.New("exit orders unavailable")
	}
	if err := scopedExitOrders(i, orders); err != nil {
		return err
	}
	positions, err := e.api.OpenPositions(ctx, i.AccountID)
	if err != nil {
		return errors.New("exit positions unavailable")
	}
	if len(positions) > 1 {
		return errors.New("unrelated exposure prevents scoped exit")
	}
	for _, p := range positions {
		if p.ContractID != i.Contract.ID || p.Type != positionType(i.Signal.Direction) || p.Size <= 0 || p.Size > parent.FillVolume {
			return errors.New("position does not match referenced trade")
		}
	}
	i.ObservedOrders, i.ObservedPositions, i.ObservedAt = orders, positions, e.now().UTC()
	return nil
}

// Validate the exact snapshot that will be mutated, not only a preceding read.
func scopedExitOrders(i *Intent, orders []projectx.Order) error {
	for _, o := range orders {
		if o.ContractID != i.Contract.ID || (o.ID != i.OrderID && (o.ParentOrderID == nil || *o.ParentOrderID != i.OrderID || o.Side != 1-side(i.Signal.Direction) || (o.Type != 1 && o.Type != 4))) {
			return errors.New("unrelated working orders prevent scoped exit")
		}
	}
	return nil
}

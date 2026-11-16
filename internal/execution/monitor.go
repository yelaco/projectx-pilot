package execution

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/yelaco/projectx-pilot/internal/projectx"
)

// Run reconciles all recovered intents before any new arming is possible.
// REST polling is restricted to active executions; idle has no account polling.
func (e *Engine) Run(ctx context.Context) {
	e.mu.Lock()
	e.ctx = ctx
	e.runRequested = false
	e.disarm()
	e.publishLocked()
	close(e.ready)
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.disarm()
		e.ctx = nil
		e.runRequested = false
		if e.quotes != nil {
			e.quotes.Close()
		}
		e.publishLocked()
	}()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-e.wake:
		}
		e.mu.Lock()
		i := e.active()
		if i != nil {
			e.lastReconcileAttempt = e.now().UTC()
			monitorCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
			if err := e.connect(i.AccountID); err != nil {
				e.disarm()
				e.runRequested = false
				e.message = "cannot connect for recovery; manage the pinned account in TopstepX"
				e.reconcileError = e.message
				e.protectionCheck = "unknown"
				i.AttentionRequired = true
				i.Message = e.message
				_ = e.persist(i)
			} else {
				e.quotes.Watch(ctx, i.Contract.ID)
				if err := e.reconcile(monitorCtx, i); err != nil {
					e.disarm()
					e.runRequested = false
					i.AttentionRequired = true
					e.message = err.Error()
					e.reconcileError = e.message
					i.Message = e.message
					_ = e.persist(i)
				} else {
					e.lastReconcileSuccess = e.now().UTC()
					e.reconcileError = ""
				}
			}
			cancel()
		}
		delay := e.monitorDelayLocked()
		e.publishLocked()
		e.mu.Unlock()
		timer.Reset(delay)
	}
}

// Retain the low-frequency backstop, but never schedule the next observation
// beyond a future entry, flatten or expiry deadline captured with this trade.
func (e *Engine) monitorDelayLocked() time.Duration {
	i := e.active()
	if i == nil {
		return 30 * time.Second
	}
	delay := 2 * time.Second
	if e.monitorInterval != nil {
		delay = e.monitorInterval()
	}
	if delay <= 0 {
		delay = 2 * time.Second
	}
	age := i.Config.maxAge()
	if i.OrderType == 1 {
		age = 30 * time.Minute
	}
	for _, deadline := range []time.Time{i.Window.LastEntry, i.Window.Flatten, i.Signal.Timestamp.Add(age)} {
		if remaining := deadline.Sub(e.now()); remaining > 0 && remaining < delay {
			delay = remaining
		}
	}
	return delay
}

func (e *Engine) reconcile(ctx context.Context, i *Intent) error {
	e.protectionCheck = "unknown"
	e.protectionCheckedAt = time.Time{}
	history, err := e.api.Orders(ctx, i.AccountID, i.Window.StartOfSession(), e.now().UTC())
	if err != nil {
		return errors.New("order reconciliation unavailable; entries paused")
	}
	orders, err := e.api.OpenOrders(ctx, i.AccountID)
	if err != nil {
		return errors.New("working orders unavailable; entries paused")
	}
	positions, err := e.api.OpenPositions(ctx, i.AccountID)
	if err != nil {
		return errors.New("positions unavailable; entries paused")
	}
	i.ObservedOrders = orders
	i.ObservedPositions = positions
	if len(positions) > 0 {
		i.ExposureObserved = true
	}
	i.ObservedAt = e.now().UTC()
	var parent *projectx.Order
	for _, o := range history {
		if o.CustomTag == i.Tag || i.OrderID != 0 && o.ID == i.OrderID {
			if parent != nil && parent.ID != o.ID {
				return e.requireAttention(i, "duplicate parent orders detected")
			}
			v := o
			parent = &v
		}
	}
	if parent == nil {
		i.State = "uncertain"
		i.HaltKind = "fault"
		e.lock(i, "submitted order absent from history; no automatic retry is permitted")
		return e.persist(i)
	}
	if parent.CustomTag != i.Tag || parent.ContractID != i.Contract.ID || parent.Size != i.Plan.Quantity || parent.Type != i.OrderType || parent.Side != side(i.Signal.Direction) {
		return e.requireAttention(i, "accepted order differs from durable intent")
	}
	i.OrderID = parent.ID
	if parent.FillVolume > 0 {
		i.ExposureObserved = true
	}
	if i.EmergencyAttempted {
		if len(positions) == 0 && len(orders) == 0 && (parent.Status == 2 || parent.Status == 3 || parent.Status == 4 || parent.Status == 5) {
			if i.FlatSince.IsZero() {
				i.FlatSince = e.now()
				return e.persist(i)
			}
			if e.now().Sub(i.FlatSince) < 3*time.Second {
				return nil
			}
			i.Message = "Account is observed flat; lockout remains for this session."
			if i.HaltKind == "session_close" {
				i.Message = "Scheduled flatten verified flat."
			}
			i.State = "closed"
			return e.persist(i)
		}
		i.AttentionRequired = true
		e.lock(i, "emergency management not verified flat; use TopstepX immediately")
		return e.persist(i)
	}
	// Limit entries have a fixed lifetime measured from the original signal,
	// independent of the incoming-alert freshness limit. Pause/window cutoffs
	// can cancel sooner. Partial fills retain protection for their filled size.
	entryMaxAge := i.Config.maxAge()
	if i.OrderType == 1 {
		entryMaxAge = 30 * time.Minute
	}
	newsErr := e.newsAllowed(e.now(), i.Config.NewsFilter)
	operationErr := e.operationAllowed(e.now(), i.Config.TradeManagerID)
	if parent.FillVolume == 0 && len(positions) == 0 && i.VerifiedPosition == nil && !i.ExposureObserved && (e.paused || operationErr != nil || newsErr != nil || !e.now().Before(i.Signal.Timestamp.Add(entryMaxAge)) || !e.now().Before(i.Window.LastEntry)) && parent.Status == 1 && !i.CancelAttempted {
		i.CancelAttempted = true
		i.Message = "Entry cancellation requested: expiry, pause or session cutoff."
		if operationErr != nil {
			i.Message = "Entry cancellation requested: " + operationErr.Error()
		}
		if newsErr != nil {
			i.Message = "Entry cancellation requested: " + newsErr.Error()
		}
		if err = e.persist(i); err != nil {
			return err
		}
		if err = e.api.Cancel(ctx, parent.ID); err != nil {
			return e.requireAttention(i, "entry cancellation uncertain")
		}
		return nil
	}
	if len(positions) > 1 {
		return e.requireAttention(i, "unexpected concurrent account exposure")
	}
	if len(positions) == 0 {
		// Flat observations are not assumed to mean a fill already closed. Require
		// explicit terminal parent state and a second flat observation after 3 sec.
		if parent.Status == 3 || parent.Status == 4 || parent.Status == 5 || parent.Status == 2 {
			if parent.FillVolume > 0 {
				trades, readErr := e.api.Trades(ctx, i.AccountID, i.Created.Add(-time.Second), e.now().UTC())
				if readErr != nil {
					return errors.New("fill history unavailable")
				}
				closed := 0
				for _, t := range trades {
					if !t.Voided && t.ContractID == i.Contract.ID && t.ProfitAndLoss != nil {
						closed += t.Size
					}
				}
				if closed < parent.FillVolume {
					return errors.New("position/fill observations disagree; awaiting full reconciliation")
				}
			}
			if i.FlatSince.IsZero() {
				i.FlatSince = e.now()
				return e.persist(i)
			}
			if e.now().Sub(i.FlatSince) < 3*time.Second {
				return nil
			}
			if len(orders) > 0 {
				return e.requireAttention(i, "flat observations with working orders; review and cancel orders locally if appropriate")
			}
			if parent.Status == 5 {
				i.State = "rejected"
			} else if parent.FillVolume == 0 {
				i.State = "cancelled"
			} else {
				i.State = "closed"
			}
			i.Message = "Terminal order and flat account verified."
			return e.persist(i)
		}
		if parent.FillVolume > 0 {
			return errors.New("filled order has no corresponding position or closing fills")
		}
		i.State = "accepted"
		i.Message = "Limit order working; no fill observed. Expires " + i.Signal.Timestamp.Add(30*time.Minute).UTC().Format(time.RFC3339) + "; pause/session cutoff may cancel sooner."
		return e.persist(i)
	}
	i.FlatSince = time.Time{}
	p := positions[0]
	if p.ContractID != i.Contract.ID || p.Size > i.Plan.Quantity || p.Type != positionType(i.Signal.Direction) || p.Size > parent.FillVolume {
		return e.requireAttention(i, "position exposure differs from observed entry fills")
	}
	if err = verifyProtection(*i, p, orders); err != nil {
		e.protectionCheck = "mismatch"
		e.protectionCheckedAt = e.now().UTC()
		return e.requireAttention(i, err.Error())
	}
	i.State = "protected"
	if parent.FillVolume < parent.Size {
		i.State = "partial"
	}
	e.protectionCheck = "verified"
	e.protectionCheckedAt = e.now().UTC()
	i.AttentionRequired = false
	i.Message = "Observed exposure has matching linked stop and target coverage."
	verified := p
	i.VerifiedPosition = &verified
	i.VerifiedAt = e.now().UTC()
	if err = e.persist(i); err != nil {
		return err
	}
	if parent.Status == 1 && parent.FillVolume > 0 && parent.FillVolume < parent.Size && (e.paused || operationErr != nil || newsErr != nil || !e.now().Before(i.Signal.Timestamp.Add(entryMaxAge)) || !e.now().Before(i.Window.LastEntry)) {
		return e.requireAttention(i, "entry restriction reached with a partial fill; review remaining entry locally")
	}
	if !e.now().Before(i.Window.Flatten) {
		return e.requireAttentionWithKind(i, "scheduled exit reminder reached; position preserved for operator", "session_close")
	}

	account, err := e.account(ctx, i.AccountID)
	if err != nil {
		return errors.New("account eligibility unavailable; entries paused")
	}
	var trades []projectx.Trade
	if !i.Policy.PerTradeOnly {
		trades, err = e.api.Trades(ctx, i.AccountID, i.Window.StartOfSession(), e.now().UTC())
		if err != nil {
			return errors.New("session history unavailable; entries paused")
		}
	}
	loss := 0.0
	if !i.Policy.PerTradeOnly {
		loss, err = sessionLoss(trades)
		if err != nil {
			return e.requireAttention(i, "session costs or P&L cannot be verified")
		}
	}
	if i.Policy.PerTradeOnly {
		i.AcknowledgedLockActive = false
		return e.persist(i)
	}
	q, err := e.quotes.Get(i.Contract.ID)
	if err != nil {
		e.disarm()
		e.runRequested = false
		e.runMessage = "Market data unavailable; entries paused. Restart locally after recovery."
		i.AttentionRequired = true
		i.Message = "Linked SL/TP verified. Market data unavailable; quote-based daily-loss and floor monitoring unavailable. Entries paused; protection monitoring continues."
		return e.persist(i)
	}
	pnl := (q.Price - p.AveragePrice) / i.Contract.TickSize * i.Contract.TickValue * float64(p.Size)
	if p.Type == 2 {
		pnl = -pnl
	}
	if !i.Policy.PerTradeOnly && loss+math.Max(0, -pnl) >= i.Policy.MaxDailyLoss {
		return e.requireAttentionWithKind(i, "daily loss limit reached", "daily_limit")
	}
	if !i.Policy.PerTradeOnly && account.Balance+pnl-i.Floor <= i.Config.RoundTripCosts*float64(p.Size) {
		i.HaltKind = "fault"
		return e.requireAttention(i, "liquidation buffer reached")
	}
	i.AcknowledgedLockActive = false
	return e.persist(i)
}
func side(direction string) int {
	if direction == "SELL" {
		return 1
	}
	return 0
}
func positionType(direction string) int {
	if direction == "SELL" {
		return 2
	}
	return 1
}
func verifyProtection(i Intent, p projectx.Position, orders []projectx.Order) error {
	stops, targets := 0, 0
	stopValue, targetValue := 0.0, 0.0
	tolerance := i.Contract.TickSize * 1e-5
	seen := map[int64]bool{}
	for _, o := range orders {
		if o.ID == i.OrderID {
			continue
		}
		if o.ID <= 0 || seen[o.ID] {
			return errors.New("duplicate or invalid protective order observation")
		}
		seen[o.ID] = true
		if o.ParentOrderID == nil || *o.ParentOrderID != i.OrderID {
			return errors.New("protective order parent link missing or different from entry")
		}
		if o.ContractID != p.ContractID {
			return errors.New("protective order contract differs from position")
		}
		if o.Side != 1-side(i.Signal.Direction) {
			return errors.New("protective order side is not opposite the entry")
		}
		if o.Status != 1 {
			return errors.New("protective order is not open")
		}
		if o.Size-o.FillVolume <= 0 {
			return errors.New("protective order has no remaining quantity")
		}

		linked := false
		pairs := 0
		for _, other := range orders {
			if ocoLinked(o, other) && other.ParentOrderID != nil && *other.ParentOrderID == i.OrderID && other.ContractID == o.ContractID && other.Side == o.Side && other.Size-other.FillVolume == o.Size-o.FillVolume && other.Status == 1 && ((o.Type == 4 && other.Type == 1) || (o.Type == 1 && other.Type == 4)) {
				if o.Type == 4 {
					if o.StopPrice == nil || other.LimitPrice == nil {
						return errors.New("bracket prices unavailable")
					}
					dir := 1.0
					if p.Type == 2 {
						dir = -1
					}
					if math.Abs((*other.LimitPrice-*o.StopPrice)-dir*float64(i.Plan.StopTicks+i.Plan.TargetTicks)*i.Contract.TickSize) > tolerance {
						return errors.New("OCO pair distance differs from plan")
					}
				}
				linked = true
				pairs++
			}
		}
		if !linked || pairs != 1 {
			return errors.New("protective OCO linkage could not be verified")
		}
		switch o.Type {
		case 4:
			if o.StopPrice == nil || !finite(*o.StopPrice) {
				return errors.New("stop price differs from planned protection")
			}
			stops += o.Size - o.FillVolume
			stopValue += *o.StopPrice * float64(o.Size-o.FillVolume)
		case 1:
			if o.LimitPrice == nil || !finite(*o.LimitPrice) {
				return errors.New("target price differs from planned protection")
			}
			targets += o.Size - o.FillVolume
			targetValue += *o.LimitPrice * float64(o.Size-o.FillVolume)
		default:
			return errors.New("unsupported protective order type")
		}
	}
	if stops != p.Size || targets != p.Size {
		return errors.New("stop or target coverage does not match filled exposure")
	}
	dir := 1.0
	if p.Type == 2 {
		dir = -1
	}
	if math.Abs(stopValue/float64(p.Size)-(p.AveragePrice-dir*float64(i.Plan.StopTicks)*i.Contract.TickSize)) > tolerance || math.Abs(targetValue/float64(p.Size)-(p.AveragePrice+dir*float64(i.Plan.TargetTicks)*i.Contract.TickSize)) > tolerance {
		return errors.New("weighted bracket prices differ from actual exposure")
	}
	return nil
}

// requireAttention only records observations and pauses entries; it has no broker mutation path.
func (e *Engine) requireAttention(i *Intent, why string) error {
	return e.requireAttentionWithKind(i, why, "fault")
}
func (e *Engine) requireAttentionWithKind(i *Intent, why, kind string) error {
	if i.HaltReason == "" {
		i.HaltReason = why
		i.HaltOrders = append([]projectx.Order(nil), i.ObservedOrders...)
	}
	i.HaltKind = kind
	i.State = "attention-required"
	i.AttentionRequired = true
	e.runRequested = false
	if i.HaltKind == "daily_limit" {
		i.State = "risk-lockout"
	}
	if i.AcknowledgedLockActive && i.AcknowledgedLockReason == why && i.RiskLockReason == "" && !i.EmergencyAttempted && i.AcknowledgedLockSnapshot == reviewSnapshot(i.ObservedPositions, i.ObservedOrders) {
		e.disarm()
		i.Message = why + ". This exact exposure was acknowledged; account unchanged. New entries remain paused until reconciliation and flatness."
		return e.persist(i)
	}
	e.lock(i, why)
	i.Message = why + ". No automatic closing or protective-order changes requested; operator attention required."
	return e.persist(i)
}

// flattenOnOperatorRequest is called only by the explicit local Flatten action.
// It persists a one-attempt latch before sending any emergency mutations.
// Ambiguous close/cancel results are never replayed by the monitor.
func (e *Engine) flattenOnOperatorRequest(ctx context.Context, i *Intent) error {
	if i.EmergencyAttempted {
		return errors.New("emergency already attempted; verify account in TopstepX")
	}
	i.EmergencyAttempted = true
	if err := e.persist(i); err != nil {
		return err
	}
	orders, err := e.api.OpenOrders(ctx, i.AccountID)
	if err != nil {
		return errors.New("emergency working-order read failed; use TopstepX immediately")
	}
	// Cancel entry parents before closing; preserve protective children while exposed.
	for _, o := range orders {
		if o.ParentOrderID == nil {
			if err = e.api.Cancel(ctx, o.ID); err != nil {
				return errors.New("emergency entry cancellation unconfirmed; use TopstepX immediately")
			}
		}
	}
	remaining, err := e.api.OpenOrders(ctx, i.AccountID)
	if err != nil {
		return errors.New("emergency cancellation verification failed; use TopstepX immediately")
	}
	for _, o := range remaining {
		if o.ParentOrderID == nil {
			return errors.New("entry still working; use TopstepX immediately")
		}
	}
	positions, err := e.api.OpenPositions(ctx, i.AccountID)
	if err != nil {
		return errors.New("emergency position read failed; use TopstepX immediately")
	}
	for _, p := range positions {
		if err = e.api.CloseContract(ctx, p.ContractID); err != nil {
			return errors.New("emergency close unconfirmed; use TopstepX immediately")
		}
	}
	flat, err := e.api.OpenPositions(ctx, i.AccountID)
	if err != nil || len(flat) != 0 {
		return errors.New("emergency close submitted but flatness unconfirmed; verify TopstepX immediately")
	}
	remaining, err = e.api.OpenOrders(ctx, i.AccountID)
	if err != nil {
		return errors.New("emergency orphan-order read failed")
	}
	for _, o := range remaining {
		if err = e.api.Cancel(ctx, o.ID); err != nil {
			return errors.New("emergency orphan cancellation unconfirmed; use TopstepX immediately")
		}
	}
	i.Message = "Emergency requests sent; waiting for flatness verification."
	return e.persist(i)
}
func (e *Engine) Flatten(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.disarm()
	i := e.active()
	if i == nil {
		return errors.New("no bot-managed active execution; manage other positions in TopstepX")
	}
	e.runRequested = false
	if i.HaltReason == "" {
		i.HaltReason = "local emergency flatten requested"
		i.HaltOrders = append([]projectx.Order(nil), i.ObservedOrders...)
	}
	i.HaltKind = "fault"
	i.State = "flattening"
	e.lock(i, "local emergency flatten requested")
	if err := e.persist(i); err != nil {
		return err
	}
	err := e.flattenOnOperatorRequest(ctx, i)
	if err != nil {
		i.AttentionRequired = true
		i.Message = err.Error()
		e.runMessage = i.Message
		_ = e.persist(i)
	}
	return err
}

// ProjectX may expose a one-way OCO relationship (e.g. target -> stop).
// Accept that edge or reciprocal edges; never infer OCO from prices alone.
func ocoLinked(a, b projectx.Order) bool {
	if a.ID == b.ID {
		return false
	}
	if a.LinkedOrderID != nil && *a.LinkedOrderID < 0 || b.LinkedOrderID != nil && *b.LinkedOrderID < 0 {
		return false
	}
	aLinked := a.LinkedOrderID != nil && *a.LinkedOrderID > 0
	bLinked := b.LinkedOrderID != nil && *b.LinkedOrderID > 0
	return (aLinked || bLinked) && (!aLinked || *a.LinkedOrderID == b.ID) && (!bLinked || *b.LinkedOrderID == a.ID)
}

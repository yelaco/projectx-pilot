// Package automation joins an authenticated inbox to the local execution engine.
// The public receiver never receives this capability or ProjectX credentials.
package automation

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"projectxbot/internal/execution"
	"projectxbot/internal/signal"
	"projectxbot/internal/storage"
)

type Engine interface {
	Status() execution.Status
	ExecuteAutomation(context.Context, string, signal.Signal, string, int) error
	Pause()
}
type Binding struct {
	ProfileID, ContractID, Strategy string
	AccountID                       int64
	Instruments                     []string
	OrderType                       int
	Approved                        bool
}
type Store interface {
	ClaimAutomation(storage.AutomationReceipt) error
	FinishAutomation(string, string, string) error
	AutomationReceipt(string) (storage.AutomationReceipt, error)
	Executions() ([]storage.ExecRow, error)
}
type Dispatcher struct {
	Engine Engine
	Store  Store
	Now    func() time.Time
}

// Process is serialized by the manager. The unique durable claim also prevents
// concurrent/crashed dispatchers from invoking the engine twice for one alert.
func (d Dispatcher) Process(ctx context.Context, b Binding, r storage.Record) error {
	if d.Now == nil {
		d.Now = time.Now
	}
	if existing, err := d.Store.AutomationReceipt(r.Signal.ID); err == nil {
		if existing.ProfileID != b.ProfileID || existing.AccountID != strconv.FormatInt(b.AccountID, 10) || existing.ContractID != b.ContractID {
			return errors.New("automation delivery identity conflict")
		}
		if existing.State == "processing" {
			// Recovery consumes the claim; absence of an intent proves no placement was
			// possible, since the engine persists intent before transmitting the order.
			rows, err := d.Store.Executions()
			if err != nil {
				return err
			}
			state, message := "interrupted", "Processing was interrupted before a durable order intent; alert will not replay."
			for _, row := range rows {
				if row.ID == r.Signal.ID {
					state = "uncertain"
					message = "A durable intent exists; order reconciliation owns recovery. No alert replay."
				}
			}
			return d.Store.FinishAutomation(r.Signal.ID, state, message)
		}
		return nil
	} else if !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	status := d.Engine.Status()
	if !status.Armed || !status.Automated || !b.Approved || status.AutomationProfileID != b.ProfileID || status.AccountID != b.AccountID {
		return errors.New("automatic execution is not armed and approved for this profile/account")
	}
	if r.ProfileID != b.ProfileID || r.AccountID != "" || r.ContractID != "" || r.Status != "pending_review" {
		return errors.New("delivery is not an unassigned alert from the bound webhook profile")
	}
	received, err := time.Parse(time.RFC3339Nano, r.ReceivedAt)
	if err != nil || received.Before(status.ArmedAt) || received.After(d.Now().Add(time.Second)) {
		return errors.New("delivery predates arming or has an invalid receipt time")
	}
	if err = d.Store.ClaimAutomation(storage.AutomationReceipt{ID: r.Signal.ID, ProfileID: b.ProfileID, AccountID: strconv.FormatInt(b.AccountID, 10), ContractID: b.ContractID}); err != nil {
		if errors.Is(err, storage.ErrDuplicate) {
			return nil
		}
		return err
	}
	finish := func(state, message string) error {
		if err := d.Store.FinishAutomation(r.Signal.ID, state, message); err != nil {
			d.Engine.Pause()
			return err
		}
		return nil
	}
	allowed := false
	for _, symbol := range b.Instruments {
		if symbol == r.Signal.Symbol {
			allowed = true
		}
	}
	if !allowed || r.Signal.Strategy != b.Strategy {
		return finish("blocked", "Signal strategy or instrument does not match the approved automation.")
	}
	if err = r.Signal.ValidateSchema(d.Now(), 5*time.Minute); err != nil {
		return finish("blocked", err.Error())
	}
	err = d.Engine.ExecuteAutomation(ctx, b.ProfileID, r.Signal, b.ContractID, b.OrderType)
	if err == nil {
		return finish("submitted", "Order submitted to ProjectX; follow execution monitoring for fills and protection.")
	}
	// A failed engine call may still have transmitted. Its durable intent is the
	// authority; never retry the alert even when no API acknowledgement arrived.
	rows, readErr := d.Store.Executions()
	if readErr != nil {
		d.Engine.Pause()
		return readErr
	}
	for _, row := range rows {
		if row.ID != r.Signal.ID {
			continue
		}
		var intent execution.Intent
		if json.Unmarshal([]byte(row.Payload), &intent) != nil {
			d.Engine.Pause()
			return errors.New("invalid persisted automation intent")
		}
		if intent.Source != "tradingview" || intent.ProfileID != b.ProfileID || intent.AccountID != b.AccountID || intent.Contract.ID != b.ContractID {
			return finish("blocked", "Signal ID conflicts with another local execution; no new order was submitted.")
		}
		return finish("uncertain", "Submission has a durable intent; reconciliation required. Never resubmit this alert.")
	}
	return finish("blocked", err.Error())
}

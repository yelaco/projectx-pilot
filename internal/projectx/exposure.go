package projectx

import (
	"context"
	"errors"
	"time"
)

// Order includes bracket linkage and partial-fill fields from the gateway schema.
type Order struct {
	ID                int64     `json:"id"`
	AccountID         int64     `json:"accountId"`
	ContractID        string    `json:"contractId"`
	Status            int       `json:"status"`
	Type              int       `json:"type"`
	Side              int       `json:"side"`
	Size              int       `json:"size"`
	FillVolume        int       `json:"fillVolume"`
	LimitPrice        *float64  `json:"limitPrice"`
	StopPrice         *float64  `json:"stopPrice"`
	FilledPrice       *float64  `json:"filledPrice"`
	CustomTag         string    `json:"customTag"`
	ParentOrderID     *int64    `json:"parentOrderId"`
	LinkedOrderID     *int64    `json:"linkedOrderId"`
	CreationTimestamp time.Time `json:"creationTimestamp"`
	UpdateTimestamp   time.Time `json:"updateTimestamp"`
}

type Position struct {
	ID                int64     `json:"id"`
	AccountID         int64     `json:"accountId"`
	ContractID        string    `json:"contractId"`
	Type              int       `json:"type"`
	Size              int       `json:"size"`
	AveragePrice      float64   `json:"averagePrice"`
	CreationTimestamp time.Time `json:"creationTimestamp"`
}

// Nil P&L denotes an opening leg, not missing closing profit. Nullable costs
// remain distinguishable from zero so callers cannot silently ignore unknown fees.
type Trade struct {
	ID                int64     `json:"id"`
	AccountID         int64     `json:"accountId"`
	ContractID        string    `json:"contractId"`
	OrderID           int64     `json:"orderId"`
	CreationTimestamp time.Time `json:"creationTimestamp"`
	Price             float64   `json:"price"`
	ProfitAndLoss     *float64  `json:"profitAndLoss"`
	Fees              *float64  `json:"fees"`
	Commissions       *float64  `json:"commissions"`
	Side              int       `json:"side"`
	Size              int       `json:"size"`
	Voided            bool      `json:"voided"`
}

func (c *Client) OpenOrders(ctx context.Context, accountID int64) ([]Order, error) {
	if accountID <= 0 {
		return nil, errors.New("positive account ID required")
	}
	var r struct {
		Orders []Order `json:"orders"`
	}
	if err := c.read(ctx, "/api/Order/searchOpen", struct {
		AccountID int64 `json:"accountId"`
	}{accountID}, &r); err != nil {
		return nil, err
	}
	// Absent/null arrays cannot establish flatness.
	if r.Orders == nil {
		return nil, errors.New("gateway omitted open orders")
	}
	for _, o := range r.Orders {
		if o.AccountID != accountID || o.ID <= 0 || o.ContractID == "" || o.Size <= 0 || o.FillVolume < 0 || o.FillVolume > o.Size {
			return nil, errors.New("invalid open order observation")
		}
	}
	return r.Orders, nil
}

func (c *Client) OpenPositions(ctx context.Context, accountID int64) ([]Position, error) {
	if accountID <= 0 {
		return nil, errors.New("positive account ID required")
	}
	var r struct {
		Positions []Position `json:"positions"`
	}
	if err := c.read(ctx, "/api/Position/searchOpen", struct {
		AccountID int64 `json:"accountId"`
	}{accountID}, &r); err != nil {
		return nil, err
	}
	if r.Positions == nil {
		return nil, errors.New("gateway omitted open positions")
	}
	for _, p := range r.Positions {
		if p.AccountID != accountID || p.ID <= 0 || p.ContractID == "" || p.Size <= 0 {
			return nil, errors.New("invalid position observation")
		}
	}
	return r.Positions, nil
}

// Trades returns observations only. Callers must establish session coverage and
// reconcile corrections before using history as an authoritative risk input.
func (c *Client) Trades(ctx context.Context, accountID int64, start, end time.Time) ([]Trade, error) {
	if accountID <= 0 || start.IsZero() || !end.After(start) {
		return nil, errors.New("account and valid history interval required")
	}
	var r struct {
		Trades []Trade `json:"trades"`
	}
	if err := c.read(ctx, "/api/Trade/search", struct {
		AccountID int64     `json:"accountId"`
		Start     time.Time `json:"startTimestamp"`
		End       time.Time `json:"endTimestamp"`
	}{accountID, start.UTC(), end.UTC()}, &r); err != nil {
		return nil, err
	}
	if r.Trades == nil {
		return nil, errors.New("gateway omitted trades")
	}
	for _, t := range r.Trades {
		if t.AccountID != accountID || t.ID <= 0 || t.CreationTimestamp.IsZero() || t.CreationTimestamp.Before(start) || t.CreationTimestamp.After(end) {
			return nil, errors.New("invalid trade history observation")
		}
	}
	return r.Trades, nil
}

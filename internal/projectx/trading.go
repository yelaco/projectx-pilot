package projectx

import (
	"context"
	"errors"
	"math"
	"time"
)

// Trading is a separate, explicitly constructed capability pinned to one account.
// It does not authorize a trade; the execution engine owns all safety gates.
type Trading struct {
	*Client
	account int64
}

func NewTrading(c *Client, account int64) (*Trading, error) {
	if c == nil || account <= 0 {
		return nil, errors.New("client and pinned account required")
	}
	return &Trading{Client: c, account: account}, nil
}
func (c *Client) StreamToken(ctx context.Context) (string, error) { return c.session(ctx) }

// Bracket.Ticks is a signed price offset relative to entry, not a positive distance.
type Bracket struct {
	Ticks int `json:"ticks"`
	Type  int `json:"type"`
}
type PlaceRequest struct {
	AccountID         int64    `json:"accountId"`
	ContractID        string   `json:"contractId"`
	Type              int      `json:"type"`
	Side              int      `json:"side"`
	Size              int      `json:"size"`
	LimitPrice        *float64 `json:"limitPrice"`
	CustomTag         string   `json:"customTag"`
	StopLossBracket   Bracket  `json:"stopLossBracket"`
	TakeProfitBracket Bracket  `json:"takeProfitBracket"`
}

// Ambiguous means a write might have reached the gateway. Never replay it.
var ErrAmbiguous = errors.New("trading result uncertain; reconcile before any further action")

func (t *Trading) write(ctx context.Context, path string, body, out any) error {
	token, err := t.session(ctx)
	if err != nil {
		return err
	} // no trading request yet
	err = t.request(ctx, path, token, body, out)
	if err == nil {
		return nil
	}
	var ae *APIError
	// Pending/unknown gateway responses are ambiguous even when HTTP succeeds.
	if errors.As(err, &ae) && ae.Status == 200 && ae.Code != 6 && ae.Code != 7 {
		return err
	}
	return ErrAmbiguous
}
func (t *Trading) Place(ctx context.Context, r PlaceRequest) (int64, error) {
	if r.AccountID != t.account || r.ContractID == "" || (r.Type != 1 && r.Type != 2) || (r.Side != 0 && r.Side != 1) || r.Size < 1 || r.Size > 100 || r.CustomTag == "" || len(r.CustomTag) > 128 || r.StopLossBracket.Type != 4 || r.TakeProfitBracket.Type != 1 {
		return 0, errors.New("invalid protected market or limit order")
	}
	stop, target := r.StopLossBracket.Ticks, r.TakeProfitBracket.Ticks
	if (r.Side == 0 && (stop >= 0 || target <= 0)) || (r.Side == 1 && (stop <= 0 || target >= 0)) || stop < -math.MaxInt32 || stop > math.MaxInt32 || target < -math.MaxInt32 || target > math.MaxInt32 {
		return 0, errors.New("bracket offsets must protect the entry side: BUY stop negative/target positive; SELL stop positive/target negative")
	}
	if r.Type == 1 && (r.LimitPrice == nil || *r.LimitPrice <= 0 || math.IsNaN(*r.LimitPrice) || math.IsInf(*r.LimitPrice, 0)) || r.Type == 2 && r.LimitPrice != nil {
		return 0, errors.New("invalid entry price for order type")
	}

	var out struct {
		OrderID int64 `json:"orderId"`
	}
	err := t.write(ctx, "/api/Order/place", r, &out)
	if err == nil && out.OrderID <= 0 {
		err = ErrAmbiguous
	}
	return out.OrderID, err
}
func (t *Trading) Cancel(ctx context.Context, id int64) error {
	if id <= 0 {
		return errors.New("positive order ID required")
	}
	return t.write(ctx, "/api/Order/cancel", struct {
		AccountID int64 `json:"accountId"`
		OrderID   int64 `json:"orderId"`
	}{t.account, id}, &struct{}{})
}
func (t *Trading) CloseContract(ctx context.Context, contract string) error {
	if contract == "" {
		return errors.New("contract required")
	}
	return t.write(ctx, "/api/Position/closeContract", struct {
		AccountID  int64  `json:"accountId"`
		ContractID string `json:"contractId"`
	}{t.account, contract}, &struct{}{})
}
func (c *Client) Orders(ctx context.Context, account int64, start, end time.Time) ([]Order, error) {
	if account <= 0 || start.IsZero() || !end.After(start) {
		return nil, errors.New("valid order history interval required")
	}
	var out struct {
		Orders []Order `json:"orders"`
	}
	err := c.read(ctx, "/api/Order/search", struct {
		AccountID int64     `json:"accountId"`
		Start     time.Time `json:"startTimestamp"`
		End       time.Time `json:"endTimestamp"`
	}{account, start.UTC(), end.UTC()}, &out)
	if err != nil {
		return nil, err
	}
	if out.Orders == nil {
		return nil, errors.New("gateway omitted order history")
	}
	for _, o := range out.Orders {
		if o.AccountID != account || o.ID <= 0 || o.ContractID == "" || o.Size <= 0 || o.FillVolume < 0 || o.FillVolume > o.Size {
			return nil, errors.New("invalid order history")
		}
	}
	return out.Orders, nil
}

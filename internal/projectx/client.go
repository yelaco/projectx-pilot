// Package projectx implements the verified, read-only subset of ProjectX Gateway.
package projectx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const BaseURL = "https://api.topstepx.com"
const maxResponse = 4 << 20

type APIError struct {
	ProviderReason  string `json:"-"` // redacted placement detail for authenticated UI only; excluded from Error/logs
	AutoOCORequired bool   // allowlisted provider diagnosis; never retain raw errorMessage
	Endpoint        string
	Status, Code    int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("ProjectX %s failed (HTTP %d, code %d)", e.Endpoint, e.Status, e.Code)
}

type result struct {
	ErrorMessage string `json:"errorMessage"`
	Success      bool   `json:"success"`
	ErrorCode    int    `json:"errorCode"`
}
type Account struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Balance   float64 `json:"balance"`
	CanTrade  bool    `json:"canTrade"`
	IsVisible bool    `json:"isVisible"`
	// nil means the gateway omitted this field; never infer simulated status.
	Simulated *bool `json:"simulated"`
}
type Contract struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	TickSize       float64 `json:"tickSize"`
	TickValue      float64 `json:"tickValue"`
	ActiveContract bool    `json:"activeContract"`
	SymbolID       string  `json:"symbolId"`
}
type Client struct {
	metadataLoading     map[string]chan struct{}
	metadataMu          sync.Mutex
	contracts           map[string]contractCache
	governor            *Governor
	http                *http.Client
	base, username, key string
	mu                  sync.Mutex // serializes token renewal; no concurrent refresh storms
	token               string
	renewAt             time.Time
}

func New(username, key string, timeout time.Duration) (*Client, error) {
	if username == "" || key == "" || timeout <= 0 {
		return nil, errors.New("credentials and positive timeout required")
	}
	// Deliberately ignore HTTP(S)_PROXY. Never follow redirects carrying credentials.
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	return &Client{http: &http.Client{Transport: tr, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, base: BaseURL, username: username, key: key}, nil
}

func (c *Client) call(ctx context.Context, path, token string, body, out any) error {
	// Defense in depth: this client cannot transmit trading requests.
	switch path {
	case "/api/Auth/loginKey", "/api/Auth/validate", "/api/Account/search", "/api/Contract/search", "/api/Contract/searchById", "/api/Contract/available":
	case "/api/Order/searchOpen", "/api/Position/searchOpen", "/api/Trade/search", "/api/Order/search":
	default:
		return errors.New("endpoint is outside read-only allowlist")
	}
	return c.request(ctx, path, token, body, out)
}

// request is private; only the pinned execution capability may invoke writes.
func (c *Client) request(ctx context.Context, path, token string, body, out any) error {
	if c.governor != nil {
		release, err := c.governor.Acquire(ctx, path)
		if err != nil {
			return err
		}
		defer release()
	}
	data, err := json.Marshal(body)
	if err != nil {
		return errors.New("request encoding failed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(data))
	if err != nil {
		return errors.New("request construction failed")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return transportFailure(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return &APIError{Endpoint: path, Status: resp.StatusCode}
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return errors.New("response read failed")
	}
	if len(data) > maxResponse {
		return errors.New("response exceeds size limit")
	}
	var envelope result
	if json.Unmarshal(data, &envelope) != nil {
		return errors.New("invalid ProjectX response")
	}
	if !envelope.Success || envelope.ErrorCode != 0 {
		return &APIError{Endpoint: path, Status: resp.StatusCode, Code: envelope.ErrorCode, ProviderReason: c.placementReason(path, envelope.ErrorMessage, token), AutoOCORequired: path == "/api/Order/place" && envelope.ErrorCode == 2 && envelope.ErrorMessage == "Brackets cannot be used with Position Brackets. You must enable Auto OCO Brackets."}
	}
	if json.Unmarshal(data, out) != nil {
		return errors.New("invalid ProjectX payload")
	}
	return nil
}

func (c *Client) session(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.renewAt) {
		return c.token, nil
	}
	var token string
	if c.token != "" {
		var r struct {
			NewToken string `json:"newToken"`
		}
		err := c.call(ctx, "/api/Auth/validate", c.token, struct{}{}, &r)
		if err == nil {
			token = r.NewToken
			if token == "" {
				return "", errors.New("renewal returned empty token")
			}
		} else {
			var ae *APIError
			if !errors.As(err, &ae) || ae.Status != http.StatusUnauthorized {
				return "", err
			}
		}
	}
	if token == "" {
		var r struct {
			Token string `json:"token"`
		}
		if err := c.call(ctx, "/api/Auth/loginKey", "", struct {
			Username string `json:"userName"`
			Key      string `json:"apiKey"`
		}{c.username, c.key}, &r); err != nil {
			return "", err
		}
		token = r.Token
	}
	if token == "" {
		return "", errors.New("authentication returned empty token")
	}
	c.token = token
	c.renewAt = time.Now().Add(23 * time.Hour)
	return token, nil
}
func (c *Client) Authenticate(ctx context.Context) error { _, err := c.session(ctx); return err }
func (c *Client) read(ctx context.Context, path string, body, out any) error {
	token, err := c.session(ctx)
	if err != nil {
		return err
	}
	err = c.call(ctx, path, token, body, out)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
		c.mu.Lock()
		if c.token == token {
			c.token = ""
		}
		c.mu.Unlock()
		// Do not retry automatically; next scheduled snapshot obtains a new session.
	}
	return err
}
func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	var r struct {
		Accounts []Account `json:"accounts"`
	}
	err := c.read(ctx, "/api/Account/search", struct {
		Active bool `json:"onlyActiveAccounts"`
	}{true}, &r)
	return r.Accounts, err
}
func (c *Client) AvailableContracts(ctx context.Context) ([]Contract, error) {
	var r struct {
		Contracts []Contract `json:"contracts"`
	}
	err := c.read(ctx, "/api/Contract/available", struct {
		Live bool `json:"live"`
	}{false}, &r)
	return r.Contracts, err
}
func (c *Client) SearchContracts(ctx context.Context, text string) ([]Contract, error) {
	if len(text) == 0 || len(text) > 64 {
		return nil, errors.New("invalid contract query")
	}
	var r struct {
		Contracts []Contract `json:"contracts"`
	}
	err := c.read(ctx, "/api/Contract/search", struct {
		Live bool   `json:"live"`
		Text string `json:"searchText"`
	}{false, text}, &r)
	return r.Contracts, err
}

type contractCache struct {
	contract Contract
	at       time.Time
}

func (c *Client) Contract(ctx context.Context, id string) (Contract, error) {
	if id == "" || len(id) > 128 {
		return Contract{}, errors.New("invalid contract ID")
	}
	for {
		c.metadataMu.Lock()
		cached, ok := c.contracts[id]
		if ok && time.Since(cached.at) < time.Minute {
			c.metadataMu.Unlock()
			return cached.contract, nil
		}
		if pending := c.metadataLoading[id]; pending != nil {
			c.metadataMu.Unlock()
			select {
			case <-pending:
				continue
			case <-ctx.Done():
				return Contract{}, ctx.Err()
			}
		}
		if c.metadataLoading == nil {
			c.metadataLoading = map[string]chan struct{}{}
		}
		c.metadataLoading[id] = make(chan struct{})
		c.metadataMu.Unlock()
		break
	}
	defer func() {
		c.metadataMu.Lock()
		close(c.metadataLoading[id])
		delete(c.metadataLoading, id)
		c.metadataMu.Unlock()
	}()
	var r struct {
		Contract Contract `json:"contract"`
	}
	err := c.read(ctx, "/api/Contract/searchById", struct {
		ID string `json:"contractId"`
	}{id}, &r)
	if err == nil && (r.Contract.ID != id || r.Contract.TickSize <= 0 || r.Contract.TickValue <= 0) {
		err = errors.New("invalid contract metadata")
	}
	if err == nil {
		c.metadataMu.Lock()
		if c.contracts == nil {
			c.contracts = map[string]contractCache{}
		}
		if len(c.contracts) >= 512 {
			clear(c.contracts)
		}
		c.contracts[id] = contractCache{r.Contract, time.Now()}
		c.metadataMu.Unlock()
	}
	return r.Contract, err
}

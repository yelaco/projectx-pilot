// Package execution provides explicitly armed, locally approved Practice trading.
package execution

import (
	"context"
	"projectxbot/internal/projectx"
	"projectxbot/internal/realtime"
	"projectxbot/internal/risk"
	"projectxbot/internal/session"
	"projectxbot/internal/signal"
	"projectxbot/internal/storage"
	"time"
)

type API interface {
	Accounts(context.Context) ([]projectx.Account, error)
	Contract(context.Context, string) (projectx.Contract, error)
	OpenOrders(context.Context, int64) ([]projectx.Order, error)
	OpenPositions(context.Context, int64) ([]projectx.Position, error)
	Orders(context.Context, int64, time.Time, time.Time) ([]projectx.Order, error)
	Trades(context.Context, int64, time.Time, time.Time) ([]projectx.Trade, error)
	Place(context.Context, projectx.PlaceRequest) (int64, error)
	Cancel(context.Context, int64) error
	CloseContract(context.Context, string) error
}
type Quotes interface {
	Watch(context.Context, string)
	Get(string) (realtime.Quote, error)
	Close()
}
type Factory func(int64) (API, Quotes, error)
type Store interface {
	ReserveExecution(storage.ExecRow) error
	SaveExecution(storage.ExecRow) error
	Executions() ([]storage.ExecRow, error)
	LockExecution(string, string, string) error
	ExecutionLock(string, string) (string, error)
}
type RunSetup struct {
	ProfileID        string    `json:"profile_id"`
	Scope            string    `json:"scope"`
	Floor            float64   `json:"floor"`
	AccountName      string    `json:"account_name"`
	CalendarRevision string    `json:"calendar_revision"`
	ConfirmedAt      time.Time `json:"confirmed_at"`
}
type Config struct {
	NewsFilter            string   `json:"news_filter,omitempty"`
	DisablePriceDeviation bool     `json:"disable_price_deviation,omitempty"`
	RunSetup              RunSetup `json:"run_setup"`
	LastVerifiedFloor     float64  `json:"last_verified_floor,omitempty"`
	Automated             bool     `json:"-"`
	AutomationProfileID   string   `json:"automation_profile_id,omitempty"`
	AutomationContractID  string   `json:"-"`
	// Legacy fields retained for reading existing configuration; ignored by execution.
	RemoteApprovalReference string        `json:"remote_approval_reference,omitempty"`
	RemoteApprovalScope     string        `json:"remote_approval_scope,omitempty"`
	RemoteApprovalAt        time.Time     `json:"remote_approval_at,omitempty"`
	SignalMaxAge            time.Duration `json:"signal_max_age,omitempty"`
	Enabled                 bool          `json:"enabled"`
	AccountID               int64         `json:"account_id"`
	TradeManagerID          string        `json:"trade_manager_id"`
	RoundTripCosts          float64       `json:"round_trip_costs"`
	SlippageTicks           int           `json:"slippage_ticks"`
	MaxQuantity             int           `json:"max_quantity"`
	MaxDeviationTicks       int           `json:"max_deviation_ticks"`
}
type Authorization struct {
	Continuous          bool
	AutomationConfirmed bool
	Window              session.Window
	Floor               float64
	PracticeConfirmed   bool
	BracketsConfirmed   bool
	FloorConfirmed      bool
	AccountName         string
}
type Intent struct {
	HaltReason         string           `json:",omitempty"`
	HaltOrders         []projectx.Order `json:",omitempty"`
	SubmissionFailure  string           `json:",omitempty"`
	HaltKind           string
	ProfileID, Source  string
	ObservedOrders     []projectx.Order
	ObservedPositions  []projectx.Position
	ObservedAt         time.Time
	OrderType          int
	ID                 string
	AccountID          int64
	Session            string
	State              string
	Message            string
	Tag                string
	OrderID            int64
	Signal             signal.Signal
	Contract           projectx.Contract
	Plan               risk.SizingPlan
	Policy             risk.Profile
	Config             Config
	Window             session.Window
	Floor              float64
	Created            time.Time
	Updated            time.Time
	EmergencyAttempted bool
	CancelAttempted    bool
	FlatSince          time.Time
}
type Preview struct {
	OrderType int
	Token     string
	Signal    signal.Signal
	Contract  projectx.Contract
	Plan      risk.SizingPlan
	Expires   time.Time
}
type SubmissionDetail struct {
	IntentID string
	Reason   string
}

type Status struct {
	SubmissionDetail         *SubmissionDetail `json:",omitempty"`
	RunRequested             bool
	RunProfileID, RunMessage string
	Automated                bool
	AutomationProfileID      string
	ArmedAt                  time.Time
	Enabled, Armed           bool
	AccountID                int64
	ArmedUntil               time.Time
	Error                    string
	Preview                  *Preview
	Intents                  []Intent
}

func (c Config) maxAge() time.Duration {
	if c.SignalMaxAge > 0 {
		return c.SignalMaxAge
	}
	return 5 * time.Minute
}

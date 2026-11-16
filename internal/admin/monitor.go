package admin

import (
	"context"
	"net/http"
	"path/filepath"
	"sort"
	"time"

	"github.com/yelaco/projectx-pilot/internal/app"
	"github.com/yelaco/projectx-pilot/internal/execution"
	"github.com/yelaco/projectx-pilot/internal/fleet"
	"github.com/yelaco/projectx-pilot/internal/realtime"
	"github.com/yelaco/projectx-pilot/internal/storage"
	"github.com/yelaco/projectx-pilot/internal/tunnel"
)

type ExecutionMonitor struct {
	SubmissionDetail         *execution.SubmissionDetail `json:",omitempty"`
	Configured               bool
	RunRequested             bool
	RunProfileID, RunMessage string
	Automated                bool
	AutomationProfileID      string
	Armed                    bool
	AccountID                int64
	ArmedUntil               time.Time
	State, Message           string
	Orders, Positions        int
	ObservedAt               time.Time
}
type Monitor struct {
	Health                       SystemHealth
	TargetJobs                   []storage.TargetJob
	UserData                     realtime.UserStatus
	Runners                      []fleet.View
	Profiles                     map[string]execution.Status
	GlobalPaused                 bool
	WebhookURLs                  map[string]string
	PublicURLKind                string
	AutomationResults            []storage.AutomationReceipt
	Execution                    ExecutionMonitor
	ConnectionActivity           []storage.Activity
	ReceivingEnabled             bool
	PublicURL                    string
	State                        app.Snapshot
	Tunnel                       tunnel.Status
	ReceiverListening            bool
	UptimeSeconds                int64
	DeliveryCounts, SignalCounts map[string]int
	Activity                     []storage.Activity
	Error                        string
}

func (m *Manager) AttachTunnelStatus(status func() tunnel.Status) { m.tunnelStatus = status }
func (m *Manager) activityStore() (*storage.Inbox, error) {
	if m.activity == nil {
		s, err := storage.Open(filepath.Join(filepath.Dir(m.credentialsPath), "activity.db"))
		if err != nil {
			return nil, err
		}
		m.activity = s
	}
	return m.activity, nil
}
func (m *Manager) Note(level, component, message string) {
	m.mu.Lock()
	s, err := m.activityStore()
	m.mu.Unlock()
	if err == nil {
		_ = s.RecordActivity(level, component, message)
	}
}
func (m *Manager) Monitor(ctx context.Context) Monitor {
	statusExecution := m.RuntimeStatus()
	summary := ExecutionMonitor{SubmissionDetail: statusExecution.SubmissionDetail, Configured: m.ExecutionConfig().Enabled || m.FleetConfigured(), RunRequested: statusExecution.RunRequested, RunProfileID: statusExecution.RunProfileID, RunMessage: statusExecution.RunMessage, Automated: statusExecution.Automated, AutomationProfileID: statusExecution.AutomationProfileID, Armed: statusExecution.Armed, AccountID: statusExecution.AccountID, ArmedUntil: statusExecution.ArmedUntil, State: "idle", Message: statusExecution.Error}
	if len(statusExecution.Intents) > 0 {
		i := statusExecution.Intents[0]
		summary.AccountID = i.AccountID
		summary.State = i.State
		summary.Orders = len(i.ObservedOrders)
		summary.Positions = len(i.ObservedPositions)
		summary.ObservedAt = i.ObservedAt
		if summary.Message == "" {
			summary.Message = i.Message
		}
	}
	routing := m.Ingress()
	results, _ := m.AutomationResults()
	v := Monitor{WebhookURLs: routing.WebhookURLs(), PublicURLKind: routing.PublicURLKind(), AutomationResults: results, Execution: summary, ReceivingEnabled: routing.Enabled, PublicURL: routing.PublicURL, Tunnel: tunnel.Status{State: "not managed"}}
	v.Runners, v.GlobalPaused = m.RunnerSnapshot()
	v.UserData = m.UserDataStatus()
	v.TargetJobs, _ = m.TargetResults()
	if len(v.TargetJobs) > 100 {
		v.TargetJobs = v.TargetJobs[:100]
	}
	for n := range v.TargetJobs {
		v.TargetJobs[n].Policy = ""
	}
	v.Profiles = m.ProfileStatuses()
	m.mu.Lock()
	service, status := m.service, m.tunnelStatus
	v.UptimeSeconds = int64(time.Since(m.startedAt).Seconds())
	audit, aerr := m.activityStore()
	remote, rerr := m.remoteStore()
	local, lerr := m.inboxStore()
	m.mu.Unlock()
	if service != nil {
		v.State = service.Snapshot()
	}
	if status != nil {
		v.Tunnel = status()
	}
	if aerr == nil {
		v.Activity, aerr = audit.Activity()
		connectionEvents, err := audit.Activity("tunnel")
		if err != nil {
			aerr = err
		} else {
			v.ConnectionActivity = append(v.ConnectionActivity, connectionEvents...)
		}
	}
	if rerr == nil {
		connectionEvents, connectionErr := remote.Activity("webhook")
		if connectionErr != nil {
			rerr = connectionErr
		} else {
			v.ConnectionActivity = append(v.ConnectionActivity, connectionEvents...)
		}
		events, err := remote.Activity()
		if err == nil {
			v.Activity = append(v.Activity, events...)
		} else {
			rerr = err
		}
		var countErr error
		v.DeliveryCounts, countErr = remote.Counts()
		if countErr != nil {
			rerr = countErr
		}
	}
	if lerr == nil {
		v.SignalCounts, lerr = local.Counts()
	}
	if aerr != nil || rerr != nil || lerr != nil {
		v.Error = "Some monitoring storage is unavailable; inspect local file permissions and disk space."
	}
	v.Activity = recentEvents(v.Activity)
	v.ConnectionActivity = recentEvents(v.ConnectionActivity)
	// Fixed loopback health check, no proxy or user-controlled destination.
	check, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(check, "GET", "http://127.0.0.1:8090/healthz", nil)
	client := http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, Timeout: 300 * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err == nil {
		v.ReceiverListening = response.StatusCode == 200
		response.Body.Close()
	}
	v.Health = buildSystemHealth(v, m.healthMeta(), m.now())
	return v
}

func recentEvents(events []storage.Activity) []storage.Activity {
	sort.SliceStable(events, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, events[i].At)
		b, _ := time.Parse(time.RFC3339Nano, events[j].At)
		return a.After(b)
	})
	if len(events) > 100 {
		return events[:100]
	}
	return events
}

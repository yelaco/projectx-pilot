package admin

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yelaco/projectx-pilot/internal/fleet"
	"github.com/yelaco/projectx-pilot/internal/risk"
)

// Hourly reports use only stored policies and existing runner/cache telemetry.
// No broker client, freshness probe, arming or execution action is involved.
func (m *Manager) runHourlyAutomationReports(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if m.fleet.Ready() {
			if err := m.processHourlyAutomationReport(); err != nil && ctx.Err() == nil {
				m.Note("WARN", "reporting", "Hourly automation status report could not be prepared or fully queued; trading state unchanged.")
			}
		}
		timer.Reset(time.Minute)
	}
}
func (m *Manager) processHourlyAutomationReport() error {
	status := m.notifications.Status()
	if !status.Enabled || !status.Configured || !status.HourlyAutomation {
		return nil
	}
	now := m.now().UTC()
	hour := now.Truncate(time.Hour)
	exists, err := m.executionStore.HourlyAutomationReportExists(hour)
	if err != nil || exists {
		return err
	}
	m.mu.Lock()
	policies := clonePolicies(m.policies)
	dashboard := "http://" + m.c.DashboardListen
	if m.c.RemoteDashboard.Origin != "" {
		dashboard = m.c.RemoteDashboard.Origin
	}
	m.mu.Unlock()
	views, paused := m.fleet.Snapshot()
	messages := automationStatusMessages(policies.Automations, policies.TradeManagers, views, paused, now, dashboard)
	claimed, err := m.executionStore.ClaimHourlyAutomationReport(hour, messages)
	if err != nil || !claimed {
		return err
	}
	queued := true
	for _, message := range messages {
		if !m.notifications.EnqueueAutomationStatus(message) {
			queued = false
		}
	}
	if !queued {
		return fmt.Errorf("hourly report queue unavailable")
	}
	return nil
}
func automationStatusMessages(profiles []Automation, managers []risk.Profile, views []fleet.View, paused bool, now time.Time, dashboard string) []string {
	profiles = append([]Automation(nil), profiles...)
	known := map[string]bool{}
	retained := map[string]bool{}
	for _, profile := range profiles {
		known[profile.ID] = true
	}
	for _, view := range views {
		for _, intent := range view.Status.Intents {
			if intent.ProfileID != "" && !healthTerminal(intent.State) && !known[intent.ProfileID] {
				known[intent.ProfileID] = true
				retained[intent.ProfileID] = true
				profiles = append(profiles, Automation{ID: intent.ProfileID, ExecutionMode: "automatic"})
			}
		}
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
	targets := map[string][]int64{}
	for _, manager := range managers {
		targets[manager.ID] = manager.AccountIDs
	}
	lines := []string{}
	for _, profile := range profiles {
		ids := targets[profile.TradeManagerID]
		if len(ids) == 0 && profile.AccountID > 0 {
			ids = []int64{profile.AccountID}
		}
		armed, managing, recovering, requested, locked, unavailable := 0, 0, 0, 0, 0, 0
		targetSet := map[int64]bool{}
		for _, account := range ids {
			targetSet[account] = true
		}
		for _, r := range views {
			retainedActive := false
			for _, intent := range r.Status.Intents {
				if intent.ProfileID == profile.ID && intent.AccountID == r.AccountID && !healthTerminal(intent.State) {
					retainedActive = true
					break
				}
			}
			if r.ProfileID != profile.ID && !retainedActive {
				continue
			}
			if retainedActive {
				managing++
			}
			if r.LockReason != "" {
				locked++
			}
			if !r.Status.MonitorRunning {
				unavailable++
				continue
			}
			if r.ProfileID != profile.ID || !targetSet[r.AccountID] {
				continue
			}
			if r.Status.Armed && !r.Status.ArmedUntil.IsZero() && now.Before(r.Status.ArmedUntil) {
				armed++
			}
			if r.Status.RunRequested {
				requested++
			}
			if r.Status.RecoveryPending {
				recovering++
			}
		}
		state := "PAUSED"
		switch {
		case profile.ExecutionMode != "automatic":
			state = "REVIEW ONLY"
		case paused:
			state = "GLOBAL PAUSE"
		case unavailable > 0:
			state = "MONITOR UNAVAILABLE"
		case armed > 0:
			state = "RUNNING"
		case recovering > 0:
			state = "WAITING FOR RECOVERY"
		case requested > 0:
			state = "WAITING FOR READINESS / SESSION"
		case managing > 0:
			state = "PAUSED / MONITORING EXECUTION"
			if retained[profile.ID] {
				state = "RETAINED / MONITORING EXECUTION"
			}
		}
		// IDs are policy-validated ASCII identifiers. Never include free-form names,
		// gateway errors, signal fields, webhook URLs or credential-bearing settings.
		lines = append(lines, fmt.Sprintf("%s — %s | targets %d, armed %d, managing %d, recovery %d, locks %d", hourlyProfileLabel(profile.ID), state, len(ids), armed, managing, recovering, locked))
	}
	if len(lines) == 0 {
		lines = []string{"No automation profiles configured."}
	}
	header := fmt.Sprintf("Captured %s | %d profiles | global pause: %t\n", now.UTC().Format(time.RFC3339), len(profiles), paused)
	chunks := []string{}
	body := ""
	for _, line := range lines {
		if len([]rune(body+line))+1 > 1200 && body != "" {
			chunks = append(chunks, strings.TrimSuffix(body, "\n"))
			body = ""
		}
		body += line + "\n"
	}
	if body != "" {
		chunks = append(chunks, strings.TrimSuffix(body, "\n"))
	}
	messages := make([]string, 0, len(chunks))
	for index, chunk := range chunks {
		messages = append(messages, header+fmt.Sprintf("Part %d/%d\n", index+1, len(chunks))+chunk+"\nDashboard: "+dashboard+"/automations")
	}
	return messages
}

func hourlyProfileLabel(id string) string {
	if len(id) < 1 || len(id) > 64 {
		return "unrecognized-profile"
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return "unrecognized-profile"
		}
	}
	return "`" + id + "`"
}

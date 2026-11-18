package attention

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const offlineHook = "https://discord.com/api/webhooks/123456/offline-test-token"

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func openTest(t *testing.T) *Discord {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "discord.json"))
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func TestDiscordPrivateSettingsValidationAndDisabledDefault(t *testing.T) {
	d := openTest(t)
	d.Publish("attention")
	if len(d.queue) != 0 {
		t.Fatal("disabled reports retained")
	}
	for _, url := range []string{"http://discord.com/api/webhooks/1/token", "https://evil.example/api/webhooks/1/token", "https://discord.com.evil.example/api/webhooks/1/token", "https://discord.com/api/webhooks/1/token?wait=true", "https://discord.com:443/api/webhooks/1/token", "https://user@discord.com/api/webhooks/1/token"} {
		if err := d.Configure(true, url, false); err == nil {
			t.Fatal("unsafe endpoint accepted", url)
		}
	}
	if err := d.Configure(true, offlineHook, false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(d.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("settings not private", err)
	}
	saved, err := Open(d.path)
	if err != nil || !saved.Status().Enabled || !saved.Status().Configured {
		t.Fatal("settings not restored", err)
	}
	encoded, _ := json.Marshal(saved.Status())
	if strings.Contains(string(encoded), "offline-test-token") {
		t.Fatal("status leaked secret")
	}
	if err = d.Configure(false, "", true); err != nil {
		t.Fatal(err)
	}
	if d.Status().Configured {
		t.Fatal("webhook not removed")
	}
}
func TestDiscordWorkerIsNonblockingBoundedAndDisablesMentions(t *testing.T) {
	d := openTest(t)
	if err := d.Configure(true, offlineHook, false); err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	d.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("wait") != "true" {
			t.Error("delivery confirmation not requested")
		}
		var body struct {
			Content string `json:"content"`
			Allowed struct {
				Parse []string `json:"parse"`
			} `json:"allowed_mentions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Allowed.Parse) != 0 || !strings.Contains(body.Content, "attention required") {
			t.Error("unsafe or missing notification content")
		}
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { d.Run(ctx); close(done) }()
	d.Publish("first report")
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("no delivery")
	}
	for n := 0; n < 100; n++ {
		d.Publish("second report")
	}
	if len(d.queue) != 1 {
		t.Fatal("duplicate reports flooded queue", len(d.queue))
	}
	if err := d.Configure(false, "", false); err != nil {
		t.Fatal(err)
	}
	close(release)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("notification worker ignored cancellation")
	}
}
func TestDiscordRateLimitAndSanitizedFailures(t *testing.T) {
	d := openTest(t)
	if err := d.Configure(true, offlineHook, false); err != nil {
		t.Fatal(err)
	}
	calls := 0
	d.client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"2"}}, Body: io.NopCloser(strings.NewReader(`{"message":"PRIVATE_RESPONSE","retry_after":2}`))}, nil
	})}
	d.send(context.Background(), offlineHook, report{message: "attention", generation: d.generation, at: time.Now()})
	if calls != 1 || time.Until(d.next) < time.Second || strings.Contains(d.Status().LastError, "PRIVATE_RESPONSE") || strings.Contains(d.Status().LastError, "offline-test-token") {
		t.Fatal("rate limit not honored or secret leaked")
	}
}
func TestDisabledOrReconfiguredQueuedReportsAreNeverDeliveredLater(t *testing.T) {
	d := openTest(t)
	_ = d.Configure(true, offlineHook, false)
	d.Publish("old report")
	_ = d.Configure(false, "", false)
	_ = d.Configure(true, offlineHook, false)
	called := make(chan struct{}, 1)
	d.client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { called <- struct{}{}; return nil, context.Canceled })}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	select {
	case <-called:
		t.Fatal("old queued report replayed")
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	<-done
}

func TestTradingWebhookIsIndependentAndUsesTradingReportContent(t *testing.T) {
	root := t.TempDir()
	system, err := Open(filepath.Join(root, "system.json"))
	if err != nil {
		t.Fatal(err)
	}
	daily, err := OpenDaily(filepath.Join(root, "daily.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = system.Configure(true, offlineHook, false); err != nil {
		t.Fatal(err)
	}
	dailyHook := "https://discord.com/api/webhooks/654321/offline-daily-token"
	if err = daily.Configure(true, dailyHook, false); err != nil {
		t.Fatal(err)
	}
	if !daily.Enqueue("Account #42 daily summary") || len(system.queue) != 0 {
		t.Fatal("trading report crossed into system queue")
	}
	daily.client = &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.Path, "offline-daily-token") || strings.Contains(req.URL.Path, "offline-test-token") {
			t.Error("wrong webhook destination")
		}
		body, _ := io.ReadAll(req.Body)
		if !strings.Contains(string(body), "daily trading report") || strings.Contains(string(body), "attention required") {
			t.Error("trading report mislabeled")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})}
	daily.send(context.Background(), dailyHook, <-daily.queue)
	if daily.Status().LastSent.IsZero() || !system.Status().LastSent.IsZero() {
		t.Fatal("delivery statuses mixed")
	}
	if err = daily.Configure(false, "", true); err != nil {
		t.Fatal(err)
	}
	if !system.Status().Configured || !system.Status().Enabled {
		t.Fatal("daily settings changed system webhook")
	}
}

func TestSystemReportLinksOnlyToLocalHealthChecklist(t *testing.T) {
	d := openTest(t)
	_ = d.Configure(true, offlineHook, false)
	if err := d.SetLocalDashboard("public.example:8080"); err == nil {
		t.Fatal("public dashboard link accepted")
	}
	if err := d.SetLocalDashboard("127.0.0.1:8181"); err != nil {
		t.Fatal(err)
	}
	d.client = &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		if !strings.Contains(string(body), "http://127.0.0.1:8181/system-health") {
			t.Error("missing local checklist link")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})}
	d.send(context.Background(), offlineHook, report{message: "Account #42: test attention", generation: d.generation, at: time.Now()})
}

func TestHourlyStatusUsesSystemWebhookWithoutAttentionLabelOrMentions(t *testing.T) {
	d := openTest(t)
	if err := d.Configure(true, offlineHook, false); err != nil {
		t.Fatal(err)
	}
	if d.EnqueueAutomationStatus("paused profiles") {
		t.Fatal("hourly reports not opt-in")
	}
	if err := d.Configure(true, "", false, true); err != nil {
		t.Fatal(err)
	}
	if !d.EnqueueAutomationStatus("`profile` — PAUSED") {
		t.Fatal("enabled hourly report not queued")
	}
	d.client = &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		var body struct {
			Content         string `json:"content"`
			AllowedMentions struct {
				Parse []string `json:"parse"`
			} `json:"allowed_mentions"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body.Content, "hourly automation status") || strings.Contains(body.Content, "attention required") || strings.Contains(body.Content, "daily trading report") || len(body.AllowedMentions.Parse) != 0 {
			t.Error("hourly report mislabeled or can mention users", body.Content)
		}
		if len([]rune(body.Content)) > 2000 || req.URL.Query().Get("wait") != "true" {
			t.Error("invalid webhook payload")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})}
	d.send(context.Background(), offlineHook, <-d.queue)
	if d.Status().LastSent.IsZero() {
		t.Fatal("confirmed status delivery not recorded")
	}
	reopened, err := Open(d.path)
	if err != nil || !reopened.Status().HourlyAutomation {
		t.Fatal("hourly setting lost on restart", err)
	}
	if err = d.Configure(true, "", false); err != nil || !d.Status().HourlyAutomation {
		t.Fatal("older client erased hourly preference")
	}
	if err = d.Configure(true, "", false, false); err != nil || d.EnqueueAutomationStatus("new report") {
		t.Fatal("disabled hourly report queued")
	}
}

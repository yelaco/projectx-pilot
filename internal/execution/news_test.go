package execution

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"projectxbot/internal/news"
	"testing"
	"time"

	"projectxbot/internal/projectx"
	"projectxbot/internal/session"
)

func TestNewsEntryGateAndProtection(t *testing.T) {
	for _, mode := range []string{"entry-blocked", "off", "submit-recheck", "pending", "filled"} {
		t.Run(mode, func(t *testing.T) {
			e, api, q, _, c, p, a, s, now := fixture(t)
			*now = time.Date(2026, 12, 8, 9, 0, 0, 0, session.Chicago)
			s.Timestamp = *now
			q.q.At = *now
			a.Window, _ = session.NormalWindow("2026-12-08")
			a.Window.Verified = true
			cachePath := filepath.Join(t.TempDir(), "news.json")
			raw := fmt.Sprintf(`{"version":1,"snapshots":[{"week":"2026-12-06","fetched_at":%q,"events":[{"title":"FOMC Statement","country":"USD","date":"2026-12-08T14:00:00-05:00","impact":"High"}]}]}`, now.UTC().Format(time.RFC3339))
			if err := os.WriteFile(cachePath, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			e.SetNewsCalendar(news.New(cachePath, nil, func() time.Time { return *now }))

			c.NewsFilter = "us_fomc"
			if mode != "entry-blocked" {
				c.NewsFilter = "off"
			}
			if err := e.Arm(context.Background(), c, p, a); err != nil {
				t.Fatal(err)
			}
			preview, err := e.PrepareOrder(context.Background(), s, "mnq", 1, 2)
			if mode == "entry-blocked" {
				if err == nil || len(api.requests) != 0 {
					t.Fatal("FOMC entry allowed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "submit-recheck" {
				e.config.NewsFilter = "us_fomc"
			}
			err = e.Submit(context.Background(), preview.Token)
			if mode == "submit-recheck" {
				if err == nil || len(api.requests) != 0 {
					t.Fatal("submission bypassed FOMC gate")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "off" {
				return
			}
			i := e.active()
			i.Config.NewsFilter = "us_fomc"
			parent := projectx.Order{ID: i.OrderID, AccountID: 42, ContractID: "mnq", Type: 1, Size: 2, Status: 1, CustomTag: i.Tag}
			api.orders = []projectx.Order{parent}
			if mode == "filled" {
				parent.Status, parent.FillVolume = 2, 2
				api.orders = protectedOrders(i, 2)
				api.positions = []projectx.Position{{ID: 1, AccountID: 42, ContractID: "mnq", Type: 1, Size: 2, AveragePrice: 20000}}
			}
			api.history = []projectx.Order{parent}
			if err := e.reconcile(context.Background(), i); err != nil {
				t.Fatal(err)
			}
			want := 1
			if mode == "filled" {
				want = 0
			}
			if api.cancels != want || api.closes != 0 {
				t.Fatal("news gate changed filled exposure", api.cancels, api.closes)
			}
		})
	}
}

func TestMissingNewsDoesNotOverrideManualOperationRules(t *testing.T) {
	e, api, _, _, c, p, a, s, _ := fixture(t)
	c.NewsFilter = "us_fomc"
	if err := e.Arm(context.Background(), c, p, a); err != nil {
		t.Fatal(err)
	}
	preview, err := e.PrepareOrder(context.Background(), s, "mnq", 1, 1)
	if err != nil {
		t.Fatal("missing calendar blocked preview", err)
	}
	if err := e.SetOperationPlan([]string{"2026-11-13"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.Submit(context.Background(), preview.Token); err == nil || len(api.requests) != 0 {
		t.Fatal("missing calendar bypassed manual day block")
	}
}

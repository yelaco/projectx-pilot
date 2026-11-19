package admin

import (
	"context"
	"encoding/json"
	"github.com/yelaco/projectx-pilot/internal/publicstatus"
	"strings"
	"testing"
	"time"
)

func TestPublicPublicationScopesAliasesAndRevocation(t *testing.T) {
	m, password, actor, _, _ := automaticFixture(t)
	defer m.Close()
	s := publicstatus.Settings{Enabled: true, Origin: "https://status.example.test", Accounts: []publicstatus.AccountConfig{{AccountID: 42, Alias: "Account A"}}}
	if m.SavePublicStatus(actor, "wrong", s) == nil {
		t.Fatal("publication bypassed password")
	}
	s.Accounts[0].AccountID = 99
	if m.SavePublicStatus(actor, password, s) == nil {
		t.Fatal("unconfigured account published")
	}
	s.Accounts[0].AccountID = 42
	if err := m.SavePublicStatus(actor, password, s); err != nil {
		t.Fatal(err)
	}
	saved, choices := m.PublicSettings()
	if len(choices) != 1 || !choices[0].Selected || saved.Accounts[0].Slug == "" || saved.Accounts[0].Slug == "42" {
		t.Fatal("missing opaque account alias", choices)
	}
	slug := saved.Accounts[0].Slug
	m.publicCredentials = m.credentials
	m.publicSnapshot = publicstatus.Snapshot{CapturedAt: m.now(), Accounts: []publicstatus.Account{{Slug: slug, Alias: "Account A", UpdatedAt: m.now(), DataAvailable: true}}}
	_, snapshot := m.PublicSnapshot()
	b, _ := json.Marshal(snapshot)
	if len(snapshot.Accounts) != 1 || strings.Contains(string(b), "account_id") {
		t.Fatal("unsafe snapshot", string(b))
	}
	s.Accounts[0].Alias = "Account B"
	if err := m.SavePublicStatus(actor, password, s); err != nil {
		t.Fatal(err)
	}
	saved, _ = m.PublicSettings()
	if saved.Accounts[0].Slug != slug {
		t.Fatal("alias change lost stable public ID")
	}
	_, snapshot = m.PublicSnapshot()
	if snapshot.Accounts[0].DataAvailable {
		t.Fatal("old data survives publication change")
	}
	m.policies.Automations = nil
	_, snapshot = m.PublicSnapshot()
	if len(snapshot.Accounts) != 0 {
		t.Fatal("revoked automation still published")
	}
	if m.ExecutionStatus().Armed || m.ExecutionStatus().RunRequested {
		t.Fatal("publication changed execution authority")
	}
}
func TestPublicStatusCredentialsAndSessionStaleness(t *testing.T) {
	m, password, actor, _, _ := automaticFixture(t)
	defer m.Close()
	s := publicstatus.Settings{Enabled: true, Origin: "https://status.example.test", Accounts: []publicstatus.AccountConfig{{AccountID: 42, Alias: "Account A"}}}
	if err := m.SavePublicStatus(actor, password, s); err != nil {
		t.Fatal(err)
	}
	settings, _ := m.PublicSettings()
	n := m.now()
	v := 10.0
	m.publicCredentials = m.credentials
	m.publicSnapshot = publicstatus.Snapshot{CapturedAt: n.Add(-24 * time.Hour), Accounts: []publicstatus.Account{{Slug: settings.Accounts[0].Slug, Alias: "Account A", Today: &v, TodayStart: n.Add(-48 * time.Hour), UpdatedAt: n.Add(-24 * time.Hour), DataAvailable: true}}}
	_, snap := m.PublicSnapshot()
	if snap.Accounts[0].Today != nil || !snap.Accounts[0].RefreshFailed {
		t.Fatal("previous session masquerades as today")
	}
	m.credentials.Username = "changed-offline"
	_, snap = m.PublicSnapshot()
	if snap.Accounts[0].DataAvailable {
		t.Fatal("old identity data survives credential change")
	}
	// With public listener disabled, collection never constructs a broker client.
	m.apiClient = nil
	m.collectPublicStatus(context.Background())
	if m.apiClient != nil {
		t.Fatal("disabled public listener fetched broker data")
	}
	_, snap = m.PublicSnapshot()
	if snap.Accounts[0].DataAvailable {
		t.Fatal("failed refresh resurrected another credential identity's financial data")
	}
}

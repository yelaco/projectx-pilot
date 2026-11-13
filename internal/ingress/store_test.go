package ingress

import (
	"path/filepath"
	"testing"
)

func TestExpressionsAndURLs(t *testing.T) {
	for _, s := range []string{"{{close}}", "{{plot_0}}", "{{plot_19}}", `{{plot("Stop Loss")}}`, ""} {
		if !Expression(s) {
			t.Fatal("valid expression rejected", s)
		}
	}
	for _, s := range []string{"{{plot_20}}", "20000", "{{close}}-5", "<script>", "$(id)"} {
		if Expression(s) {
			t.Fatal("unsafe expression accepted", s)
		}
	}
	if !PublicURL("https://example.test") || PublicURL("http://example.test") || PublicURL("https://example.test/path") {
		t.Fatal("bad public URL validation")
	}
}
func TestStoreSnapshotsAreIndependent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ingress.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Update(func(c *Settings) error { c.Routes = []Route{{ID: "one", Symbols: []string{"MNQ"}}}; return nil }); err != nil {
		t.Fatal(err)
	}
	v := s.Snapshot()
	v.Routes[0].Symbols[0] = "MGC"
	if s.Snapshot().Routes[0].Symbols[0] != "MNQ" {
		t.Fatal("state alias")
	}
}

func TestPermanentWebhookURLsPersistAndStayScoped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ingress.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Update(func(v *Settings) error {
		v.PublicURL = "https://alerts.example.com/"
		v.Routes = []Route{{ID: "swift", Token: "private-delivery-token"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	v := reopened.Snapshot()
	urls := v.WebhookURLs()
	if urls["swift"] != "https://alerts.example.com/tradingview/swift" || urls["unknown"] != "" || v.PublicURLKind() != "Permanent hostname" {
		t.Fatal("incorrect scoped permanent URL", urls)
	}
	// Disabling receiving does not erase the configured address or change tokens.
	v.Enabled = false
	if v.WebhookURLs()["swift"] != urls["swift"] {
		t.Fatal("disabled receiver lost saved URL")
	}
	for _, base := range []string{"", "http://example.com", "https://user:secret@example.com", "https://example.com/path"} {
		v.PublicURL = base
		if len(v.WebhookURLs()) != 0 {
			t.Fatal("unusable base address produced endpoints")
		}
	}
}

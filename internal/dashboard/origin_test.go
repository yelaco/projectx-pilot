package dashboard

import (
	"net/http/httptest"
	"testing"
)

func TestSameOriginForms(t *testing.T) {
	for _, tc := range []struct {
		name, origin, site, referrer string
		want                         bool
	}{
		{name: "explicit local", origin: "http://127.0.0.1:8080", want: true},
		{name: "opaque same-origin browser form", origin: "null", site: "same-origin", want: true},
		{name: "missing origin browser form", site: "same-origin", want: true},
		{name: "same-origin referrer fallback", referrer: "http://127.0.0.1:8080/", want: true},
		{name: "opaque without evidence", origin: "null"},
		{name: "no evidence"},
		{name: "foreign origin", origin: "http://evil.test", site: "same-origin"},
		{name: "foreign referrer", referrer: "http://evil.test/"},
		{name: "host prefix spoof", referrer: "http://127.0.0.1:8080.evil.test/"},
		{name: "different port", origin: "http://127.0.0.1:8081"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://127.0.0.1:8080/settings", nil)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Sec-Fetch-Site", tc.site)
			r.Header.Set("Referer", tc.referrer)
			if got := sameOriginForm(r); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

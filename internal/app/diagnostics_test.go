package app

import (
	"errors"
	"strings"
	"testing"

	"projectxbot/internal/projectx"
)

func TestRefreshDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&projectx.APIError{Endpoint: "/api/Auth/loginKey", Status: 200, Code: 3}, "platform login"},
		{&projectx.APIError{Endpoint: "/api/Auth/loginKey", Status: 200, Code: 9}, "subscription"},
		{&projectx.APIError{Status: 429}, "Rate limited"},
		{&projectx.TransportError{Kind: "dns"}, "DNS"},
		{&projectx.TransportError{Kind: "timeout"}, "timed out"},
		{errAccountNotFound, "account ID to 0"},
		{errors.New("secret token upstream payload"), "service logs"},
	} {
		got := refreshFailure(tc.err)
		if !strings.Contains(got, tc.want) || strings.Contains(got, "secret") {
			t.Fatalf("unsafe or unhelpful diagnostic: %s", got)
		}
	}
}

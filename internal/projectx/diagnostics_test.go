package projectx

import (
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestTransportDiagnosticsRedactUnderlyingDetails(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&net.DNSError{Name: "secret", Err: "secret"}, "DNS"},
		{&net.DNSError{Name: "secret", Err: "secret", IsTimeout: true}, "DNS"},
		{fmt.Errorf("secret transport error"), "internet connection"},
	} {
		err := transportFailure(tc.err)
		if strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), tc.want) {
			t.Fatal(err)
		}
	}
}

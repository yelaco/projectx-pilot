package projectx

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"unicode"
)

// TransportError deliberately retains no underlying URL, headers, or credentials.
type TransportError struct{ Kind string }

func (e *TransportError) Error() string { return e.SafeMessage() }
func (e *TransportError) SafeMessage() string {
	switch e.Kind {
	case "dns":
		return "Cannot resolve the ProjectX API hostname. Check this laptop's DNS and internet connection."
	case "timeout":
		return "The ProjectX API request timed out. Check connectivity or try refreshing again."
	case "tls":
		return "The ProjectX HTTPS certificate could not be verified. Check the system clock and certificate store."
	default:
		return "Cannot connect to the ProjectX API. Check this laptop's internet connection."
	}
}
func transportFailure(err error) error {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return &TransportError{Kind: "dns"}
	}
	var cert x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	if errors.As(err, &cert) || errors.As(err, &invalid) || errors.As(err, &hostname) {
		return &TransportError{Kind: "tls"}
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return &TransportError{Kind: "timeout"}
	}
	return &TransportError{Kind: "network"}
}
func (e *APIError) SafeMessage() string {
	detail := fmt.Sprintf("ProjectX returned HTTP %d, API code %d.", e.Status, e.Code)
	if e.AutoOCORequired {
		return detail + " Enable Auto OCO Brackets for the selected account in TopstepX Settings → Risk Settings while flat."
	}
	if e.Endpoint == "/api/Auth/loginKey" {
		switch e.Code {
		case 3:
			return detail + " Authentication failed. Check your exact TopstepX platform login and API key."
		case 7:
			return detail + " Sign in to the official platform and complete the required agreements."
		case 9:
			return detail + " Check your API subscription and its link to your TopstepX profile."
		case 10:
			return detail + " API-key authentication is disabled by the firm; contact official support."
		}
	}
	switch e.Status {
	case 401:
		return detail + " Authentication expired or was rejected. Refresh again; if it persists, review your credentials."
	case 403:
		return detail + " Access was denied. Check your account and API access permissions."
	case 429:
		return detail + " Rate limited. Wait for the next scheduled refresh."
	}
	return detail + " Check the official platform and API service status."
}

var jwtInReason = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)

// placementReason never enters Error(), SafeMessage(), a persisted intent or logs.
// Only this bounded field is shown as escaped text to authenticated administrators.
func (c *Client) placementReason(path, message, token string) string {
	if path != "/api/Order/place" {
		return ""
	}
	for _, secret := range []string{c.key, c.username, token} {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	message = jwtInReason.ReplaceAllString(message, "[redacted]")
	message = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, message)
	chars := []rune(message)
	if len(chars) > 1024 {
		message = string(chars[:1024]) + "…"
	}
	return strings.TrimSpace(message)
}

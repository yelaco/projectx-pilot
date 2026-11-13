package app

import (
	"context"
	"errors"
)

var errAccountNotFound = errors.New("configured account is not present in active accounts")

func refreshFailure(err error) string {
	var safe interface{ SafeMessage() string }
	var message string
	switch {
	case errors.Is(err, errAccountNotFound):
		message = "Configured account was not found among active accounts. Set the account ID to 0 for discovery, then select an ID from the account list."
	case errors.Is(err, context.DeadlineExceeded):
		message = "ProjectX refresh timed out. Check your connection and try again."
	case errors.Is(err, context.Canceled):
		message = "ProjectX refresh was cancelled."
	case errors.As(err, &safe):
		message = safe.SafeMessage()
	default:
		message = "ProjectX returned an invalid response or the refresh failed. Check the local service logs."
	}
	return message + " Previously displayed data may be stale."
}

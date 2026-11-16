package tests

import (
	"context"
	"os"
	"github.com/yelaco/projectx-pilot/internal/projectx"
	"testing"
	"time"
)

func TestProjectXReadOnly(t *testing.T) {
	if os.Getenv("PROJECTX_INTEGRATION") != "1" {
		t.Skip("set PROJECTX_INTEGRATION=1 for authenticated read-only network test")
	}
	c, err := projectx.New(os.Getenv("PROJECTX_USERNAME"), os.Getenv("PROJECTX_API_KEY"), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = c.Authenticate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Accounts(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = c.AvailableContracts(ctx); err != nil {
		t.Fatal(err)
	}
}

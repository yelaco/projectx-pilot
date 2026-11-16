package realtime

import (
	"fmt"
	"testing"
	"time"
)

func TestSignalRFramingAndQuotes(t *testing.T) {
	p := protocol{contract: "mnq"}
	at := time.Now().UTC().Format(time.RFC3339Nano)
	data := fmt.Sprintf("{}\x1e{\"type\":3,\"invocationId\":\"quote\"}\x1e{\"type\":1,\"target\":\"GatewayQuote\",\"arguments\":[\"mnq\",{\"lastPrice\":20000,\"lastUpdated\":\"%s\"}]}\x1e", at)
	var all []Quote
	for n := 0; n < len(data); n += 7 {
		end := n + 7
		if end > len(data) {
			end = len(data)
		}
		q, _, err := p.consume([]byte(data[n:end]))
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, q...)
	}
	if len(all) != 1 || all[0].Price != 20000 || !p.handshake {
		t.Fatal(all)
	}
	f := New(nil)
	f.contract = "mnq"
	f.quote = all[0]
	if _, err := f.Get("mnq"); err != nil {
		t.Fatal(err)
	}
	f.quote.At = time.Now().Add(-time.Minute)
	if _, err := f.Get("mnq"); err == nil {
		t.Fatal("stale quote accepted")
	}
	if _, err := f.Get("mgc"); err == nil {
		t.Fatal("wrong contract accepted")
	}
}
func TestSignalRErrors(t *testing.T) {
	for _, data := range []string{"{\"error\":\"secret\"}\x1e", "{}\x1e{\"type\":3,\"error\":\"denied\"}\x1e", "{}\x1e{\"type\":7}\x1e"} {
		p := protocol{contract: "mnq"}
		if _, _, err := p.consume([]byte(data)); err == nil {
			t.Fatal("hub error ignored")
		}
	}
	p := protocol{contract: "mnq"}
	if _, _, err := p.consume(make([]byte, (1<<20)+1)); err == nil {
		t.Fatal("unbounded frame accepted")
	}
}
func TestMarketHubIgnoresOtherContractsAndInvalidPrice(t *testing.T) {
	p := protocol{contract: "mnq"}
	data := "{}\x1e{\"type\":1,\"target\":\"GatewayQuote\",\"arguments\":[\"mgc\",{\"lastPrice\":20000,\"lastUpdated\":\"2026-11-13T12:00:00Z\"}]}\x1e{\"type\":1,\"target\":\"GatewayQuote\",\"arguments\":[\"mnq\",{\"lastPrice\":0}]}\x1e"
	quotes, _, err := p.consume([]byte(data))
	if err != nil || len(quotes) != 0 {
		t.Fatal(quotes, err)
	}
}

func TestInspectReportsCachedQuoteAgeWithoutChangingFeed(t *testing.T) {
	f := New(nil)
	f.contract = "mnq"
	f.state = "connected"
	f.quote = Quote{Price: 20000, At: time.Now().Add(-time.Minute)}
	before := f.quote
	state := f.Inspect("mnq")
	if state.Fresh || state.At != before.At || state.Contract != "mnq" || f.quote != before || f.done != nil {
		t.Fatal("diagnostic inspection changed/warmed feed or accepted stale quote")
	}
	if state = f.Inspect("other"); state.Fresh || !state.At.IsZero() {
		t.Fatal("wrong contract inherited another feed's quote")
	}
}

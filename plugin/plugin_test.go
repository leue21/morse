package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"salert/internal/testutil"
)

// --- BTCPrice tests ---

func btcPriceJSON(price, change24h float64) string {
	b, _ := json.Marshal(map[string]any{
		"bitcoin": map[string]any{"usd": price, "usd_24h_change": change24h},
	})
	return string(b)
}

func TestBTCPriceName(t *testing.T) {
	p := NewBTCPrice(0, 0, 0, 0)
	if p.Name() != "btcprice" {
		t.Errorf("Name() = %q, want %q", p.Name(), "btcprice")
	}
}

func TestBTCPriceAboveThreshold(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("GET", "/", 200, btcPriceJSON(105000, 1.0)).
		Start()

	p := NewBTCPrice(100000, 0, 0, 0)
	p.apiURL = srv.URL()

	alerts, err := p.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("got %d alerts, want 1", len(alerts))
	}
	if alerts[0].Title != "BTC Price Alert" {
		t.Errorf("title = %q", alerts[0].Title)
	}
}

func TestBTCPriceBelowThreshold(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("GET", "/", 200, btcPriceJSON(45000, -1.0)).
		Start()

	p := NewBTCPrice(0, 50000, 0, 0)
	p.apiURL = srv.URL()

	alerts, err := p.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("got %d alerts, want 1", len(alerts))
	}
}

func TestBTCPriceNoAlert(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("GET", "/", 200, btcPriceJSON(75000, 0.5)).
		Start()

	p := NewBTCPrice(100000, 50000, 0, 0)
	p.apiURL = srv.URL()

	alerts, err := p.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(alerts) != 0 {
		t.Fatalf("got %d alerts, want 0", len(alerts))
	}
}

func TestBTCPriceCooldown(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("GET", "/", 200, btcPriceJSON(105000, 1.0)).
		Start()

	p := NewBTCPrice(100000, 0, 0, 1*time.Hour)
	p.apiURL = srv.URL()

	// First check should alert.
	alerts, _ := p.Check(context.Background())
	if len(alerts) != 1 {
		t.Fatalf("first check: got %d alerts, want 1", len(alerts))
	}

	// Second check within cooldown should not alert.
	alerts, _ = p.Check(context.Background())
	if len(alerts) != 0 {
		t.Fatalf("second check: got %d alerts, want 0 (cooldown)", len(alerts))
	}
}

func TestBTCPriceAPIError(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("GET", "/", http.StatusTooManyRequests, "").
		Start()

	p := NewBTCPrice(100000, 0, 0, 0)
	p.apiURL = srv.URL()

	_, err := p.Check(context.Background())
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestBTCPriceCancelledContext(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("GET", "/", 200, btcPriceJSON(105000, 1.0)).
		Start()

	p := NewBTCPrice(100000, 0, 0, 0)
	p.apiURL = srv.URL()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := p.Check(ctx)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// --- BTCPrice 24h change tests ---

func TestBTCPriceChangeAboveThreshold(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("GET", "/", 200, btcPriceJSON(75000, 8.0)).
		Start()

	p := NewBTCPrice(0, 0, 5.0, 0)
	p.apiURL = srv.URL()

	alerts, err := p.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("got %d alerts, want 1", len(alerts))
	}
	if alerts[0].Title != "BTC 24h Change Alert" {
		t.Errorf("title = %q, want %q", alerts[0].Title, "BTC 24h Change Alert")
	}
}

func TestBTCPriceChangeNegativeAboveThreshold(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("GET", "/", 200, btcPriceJSON(75000, -6.0)).
		Start()

	p := NewBTCPrice(0, 0, 5.0, 0)
	p.apiURL = srv.URL()

	alerts, err := p.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("got %d alerts, want 1", len(alerts))
	}
	if alerts[0].Title != "BTC 24h Change Alert" {
		t.Errorf("title = %q, want %q", alerts[0].Title, "BTC 24h Change Alert")
	}
}

func TestBTCPriceChangeBelowThreshold(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("GET", "/", 200, btcPriceJSON(75000, 2.0)).
		Start()

	p := NewBTCPrice(0, 0, 5.0, 0)
	p.apiURL = srv.URL()

	alerts, err := p.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(alerts) != 0 {
		t.Fatalf("got %d alerts, want 0", len(alerts))
	}
}

// --- BTCPrice Describe tests ---

func TestBTCPriceDescribeAllFields(t *testing.T) {
	p := NewBTCPrice(100000, 50000, 5.0, 30*time.Minute)
	desc := p.Describe()
	for _, want := range []string{"Above: $100000.00", "Below: $50000.00", "24h change: 5.00%", "Cooldown: 30m"} {
		if !strings.Contains(desc, want) {
			t.Errorf("Describe() missing %q, got:\n%s", want, desc)
		}
	}
}

func TestBTCPriceDescribePartial(t *testing.T) {
	p := NewBTCPrice(100000, 0, 0, 0)
	desc := p.Describe()
	if !strings.Contains(desc, "Above: $100000.00") {
		t.Errorf("Describe() missing Above, got:\n%s", desc)
	}
	for _, absent := range []string{"Below:", "24h change:", "Cooldown:"} {
		if strings.Contains(desc, absent) {
			t.Errorf("Describe() should not contain %q, got:\n%s", absent, desc)
		}
	}
}

func TestBTCPriceDescribeEmpty(t *testing.T) {
	p := NewBTCPrice(0, 0, 0, 0)
	if desc := p.Describe(); desc != "" {
		t.Errorf("Describe() = %q, want empty", desc)
	}
}

// TooGoodToGo tests are in toogoodtogo_test.go

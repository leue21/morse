package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"
)

// BTCPrice monitors Bitcoin price and alerts when thresholds are crossed.
type BTCPrice struct {
	aboveUSD      float64
	belowUSD      float64
	changePercent float64
	cooldown      time.Duration
	client        *http.Client
	lastSent      time.Time
	apiURL        string // overridable for testing
}

func NewBTCPrice(aboveUSD, belowUSD, changePercent float64, cooldown time.Duration) *BTCPrice {
	return &BTCPrice{
		aboveUSD:      aboveUSD,
		belowUSD:      belowUSD,
		changePercent: changePercent,
		cooldown:      cooldown,
		client:        &http.Client{Timeout: 10 * time.Second},
		apiURL:        "https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=usd&include_24hr_change=true",
	}
}

func (b *BTCPrice) Name() string { return "btcprice" }

func (b *BTCPrice) Describe() string {
	var s string
	if b.aboveUSD > 0 {
		s += fmt.Sprintf("  Above: $%.2f\n", b.aboveUSD)
	}
	if b.belowUSD > 0 {
		s += fmt.Sprintf("  Below: $%.2f\n", b.belowUSD)
	}
	if b.changePercent > 0 {
		s += fmt.Sprintf("  24h change: %.2f%%\n", b.changePercent)
	}
	if b.cooldown > 0 {
		s += fmt.Sprintf("  Cooldown: %s\n", b.cooldown)
	}
	return s
}

func (b *BTCPrice) Check(ctx context.Context) ([]Alert, error) {
	price, change24h, err := b.fetchPrice(ctx)
	if err != nil {
		return nil, err
	}

	if b.cooldown > 0 && time.Since(b.lastSent) < b.cooldown {
		return nil, nil
	}

	var alerts []Alert

	if b.aboveUSD > 0 && price >= b.aboveUSD {
		alerts = append(alerts, Alert{
			Title:   "BTC Price Alert",
			Message: fmt.Sprintf("Bitcoin is at $%.2f (above $%.2f threshold)", price, b.aboveUSD),
		})
	}

	if b.belowUSD > 0 && price <= b.belowUSD {
		alerts = append(alerts, Alert{
			Title:   "BTC Price Alert",
			Message: fmt.Sprintf("Bitcoin is at $%.2f (below $%.2f threshold)", price, b.belowUSD),
		})
	}

	if b.changePercent > 0 && math.Abs(change24h) >= b.changePercent {
		direction := "up"
		if change24h < 0 {
			direction = "down"
		}
		alerts = append(alerts, Alert{
			Title:   "BTC 24h Change Alert",
			Message: fmt.Sprintf("Bitcoin moved %s %.2f%% in the last 24h (threshold: %.2f%%)", direction, math.Abs(change24h), b.changePercent),
		})
	}

	if len(alerts) > 0 {
		b.lastSent = time.Now()
	}

	return alerts, nil
}

func (b *BTCPrice) fetchPrice(ctx context.Context) (float64, float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.apiURL, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("creating request: %w", err)
	}

	resp, err := b.client.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("fetching BTC price: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("coingecko API returned %d", resp.StatusCode)
	}

	var result struct {
		Bitcoin struct {
			USD          float64 `json:"usd"`
			USD24hChange float64 `json:"usd_24h_change"`
		} `json:"bitcoin"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, 0, fmt.Errorf("decoding response: %w", err)
	}

	return result.Bitcoin.USD, result.Bitcoin.USD24hChange, nil
}

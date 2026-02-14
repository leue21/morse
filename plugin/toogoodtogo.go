package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TooGoodToGo monitors TooGoodToGo favorites for available surprise bags.
type TooGoodToGo struct {
	email   string
	dataDir string // directory for tgtg_tokens.json
	client  *http.Client

	userAgent string

	// auth state
	accessToken  string
	refreshToken string
	userID       string
	cookie       string // datadome cookie
	lastRefresh  time.Time

	// dedup — entry removed when stock drops to 0 so restock re-alerts
	seen map[string]bool

	// testing hooks
	baseURL     string // default "https://apptoogoodtogo.com/api"
	datadomeURL string // default "https://api-sdk.datadome.co/sdk/"
}

type tgtgItem struct {
	ItemID         string `json:"item_id"`
	StoreName      string `json:"store_name"`
	DisplayName    string `json:"display_name"`
	ItemsAvailable int    `json:"items_available"`
}

type tgtgTokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	UserID       string    `json:"user_id"`
	Cookie       string    `json:"cookie"`
	LastRefresh  time.Time `json:"last_refresh"`
}

func NewTooGoodToGo(email, dataDir string) *TooGoodToGo {
	t := &TooGoodToGo{
		email:       email,
		dataDir:     dataDir,
		client:      &http.Client{Timeout: 30 * time.Second},
		userAgent:   "TGTG/25.1.1 Dalvik/2.1.0 (Linux; Android 12; SM-G991B)",
		seen:        make(map[string]bool),
		baseURL:     "https://apptoogoodtogo.com/api",
		datadomeURL: "https://api-sdk.datadome.co/sdk/",
	}
	t.loadTokens()
	return t
}

func (t *TooGoodToGo) Name() string { return "toogoodtogo" }

func (t *TooGoodToGo) Describe() string {
	return fmt.Sprintf("  Email: %s\n  Mode: favorites\n", t.email)
}

func (t *TooGoodToGo) Check(ctx context.Context) ([]Alert, error) {
	// Step 1: authenticate if needed
	if t.accessToken == "" {
		if err := t.authenticate(ctx); err != nil {
			return nil, fmt.Errorf("tgtg auth: %w", err)
		}
	}

	// Step 2: refresh tokens if older than 4 hours
	if time.Since(t.lastRefresh) > 4*time.Hour {
		if err := t.refreshTokens(ctx); err != nil {
			slog.Warn("tgtg token refresh failed, will re-auth next cycle", "error", err)
			t.accessToken = ""
			return nil, nil
		}
	}

	// Step 3: get favorites
	items, err := t.getFavorites(ctx)
	if err != nil {
		return nil, fmt.Errorf("tgtg favorites: %w", err)
	}

	// Step 4: generate alerts
	var alerts []Alert
	for _, item := range items {
		if item.ItemsAvailable > 0 {
			if !t.seen[item.ItemID] {
				alerts = append(alerts, Alert{
					Title:   fmt.Sprintf("TGTG: %s", item.StoreName),
					Message: fmt.Sprintf("%s — %d bag(s) available", item.DisplayName, item.ItemsAvailable),
				})
				t.seen[item.ItemID] = true
			}
		} else {
			delete(t.seen, item.ItemID)
		}
	}

	return alerts, nil
}

// authenticate performs the email OTP auth flow.
func (t *TooGoodToGo) authenticate(ctx context.Context) error {
	if err := t.fetchDataDomeCookie(ctx); err != nil {
		return fmt.Errorf("datadome: %w", err)
	}

	// Step 1: request email OTP
	body := map[string]any{
		"device_type": "ANDROID",
		"email":       t.email,
	}
	resp, err := t.apiPost(ctx, "/auth/v5/authByEmail", body)
	if err != nil {
		return fmt.Errorf("authByEmail: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("authByEmail: status %d", resp.StatusCode)
	}

	var authResp struct {
		PollingID string `json:"polling_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return fmt.Errorf("authByEmail decode: %w", err)
	}

	slog.Info("tgtg: check your email and click the TGTG login link", "email", t.email)

	// Step 2: poll for completion
	pollBody := map[string]any{
		"device_type": "ANDROID",
		"email":       t.email,
		"request_polling_id": authResp.PollingID,
	}

	for i := 0; i < 24; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}

		resp, err := t.apiPost(ctx, "/auth/v5/authByRequestPollingId", pollBody)
		if err != nil {
			continue
		}

		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			var tokenResp struct {
				AccessToken  string `json:"access_token"`
				RefreshToken string `json:"refresh_token"`
				StartupData  struct {
					User struct {
						UserID string `json:"user_id"`
					} `json:"user"`
				} `json:"startup_data"`
			}
			if err := json.Unmarshal(data, &tokenResp); err != nil {
				return fmt.Errorf("poll decode: %w", err)
			}
			t.accessToken = tokenResp.AccessToken
			t.refreshToken = tokenResp.RefreshToken
			t.userID = tokenResp.StartupData.User.UserID
			t.lastRefresh = time.Now()
			t.saveTokens()
			slog.Info("tgtg: authenticated successfully")
			return nil
		}
		// 202 = still pending, keep polling
	}

	return fmt.Errorf("auth polling timed out after 24 attempts")
}

// refreshTokens refreshes the access token.
func (t *TooGoodToGo) refreshTokens(ctx context.Context) error {
	body := map[string]any{
		"refresh_token": t.refreshToken,
	}
	resp, err := t.apiPost(ctx, "/token/v1/refresh", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		t.accessToken = ""
		t.refreshToken = ""
		return fmt.Errorf("refresh returned 401")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("refresh: status %d", resp.StatusCode)
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("refresh decode: %w", err)
	}

	t.accessToken = tokenResp.AccessToken
	t.refreshToken = tokenResp.RefreshToken
	t.lastRefresh = time.Now()
	t.saveTokens()
	return nil
}

// getFavorites fetches the user's favorite items.
func (t *TooGoodToGo) getFavorites(ctx context.Context) ([]tgtgItem, error) {
	return t.getFavoritesWithRetry(ctx, true, true)
}

func (t *TooGoodToGo) getFavoritesWithRetry(ctx context.Context, retry401, retry403 bool) ([]tgtgItem, error) {
	body := map[string]any{
		"user_id":        t.userID,
		"favorites_only": true,
		"page_size":      50,
		"page":           1,
	}
	resp, err := t.apiPost(ctx, "/item/v8/", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized && retry401 {
		resp.Body.Close()
		if err := t.refreshTokens(ctx); err != nil {
			return nil, fmt.Errorf("refresh after 401: %w", err)
		}
		return t.getFavoritesWithRetry(ctx, false, retry403)
	}

	if resp.StatusCode == http.StatusForbidden && retry403 {
		resp.Body.Close()
		if err := t.fetchDataDomeCookie(ctx); err != nil {
			return nil, fmt.Errorf("datadome refresh after 403: %w", err)
		}
		return t.getFavoritesWithRetry(ctx, retry401, false)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("favorites: status %d", resp.StatusCode)
	}

	var result struct {
		Items []struct {
			Item struct {
				ItemID string `json:"item_id"`
			} `json:"item"`
			Store struct {
				StoreName string `json:"store_name"`
			} `json:"store"`
			DisplayName    string `json:"display_name"`
			ItemsAvailable int    `json:"items_available"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("favorites decode: %w", err)
	}

	items := make([]tgtgItem, len(result.Items))
	for i, r := range result.Items {
		items[i] = tgtgItem{
			ItemID:         r.Item.ItemID,
			StoreName:      r.Store.StoreName,
			DisplayName:    r.DisplayName,
			ItemsAvailable: r.ItemsAvailable,
		}
	}
	return items, nil
}

// fetchDataDomeCookie obtains a DataDome cookie for bot protection bypass.
func (t *TooGoodToGo) fetchDataDomeCookie(ctx context.Context) error {
	form := url.Values{
		"ddk":      {"A2B3C4D5E6"},
		"ddv":      {"4.10.2"},
		"ddsrc":    {"sdk"},
		"responsePage": {"origin"},
		"ddtype":   {"l"},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", t.datadomeURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", t.userAgent)

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var ddResp struct {
		Cookie string `json:"cookie"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ddResp); err != nil {
		return fmt.Errorf("datadome decode: %w", err)
	}
	t.cookie = ddResp.Cookie
	return nil
}

// apiPost makes an authenticated POST request to the TGTG API.
func (t *TooGoodToGo) apiPost(ctx context.Context, path string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", t.baseURL+path,
		strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", t.userAgent)
	if t.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+t.accessToken)
	}
	if t.cookie != "" {
		req.Header.Set("Cookie", "datadome="+t.cookie)
	}

	return t.client.Do(req)
}

// loadTokens reads saved tokens from disk.
func (t *TooGoodToGo) loadTokens() {
	path := filepath.Join(t.dataDir, "tgtg_tokens.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return // no saved tokens, that's fine
	}
	var tokens tgtgTokens
	if err := json.Unmarshal(data, &tokens); err != nil {
		slog.Warn("tgtg: failed to parse token file", "error", err)
		return
	}
	t.accessToken = tokens.AccessToken
	t.refreshToken = tokens.RefreshToken
	t.userID = tokens.UserID
	t.cookie = tokens.Cookie
	t.lastRefresh = tokens.LastRefresh
}

// saveTokens writes current tokens to disk.
func (t *TooGoodToGo) saveTokens() {
	path := filepath.Join(t.dataDir, "tgtg_tokens.json")
	tokens := tgtgTokens{
		AccessToken:  t.accessToken,
		RefreshToken: t.refreshToken,
		UserID:       t.userID,
		Cookie:       t.cookie,
		LastRefresh:  t.lastRefresh,
	}
	data, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		slog.Error("tgtg: failed to marshal tokens", "error", err)
		return
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		slog.Error("tgtg: failed to save tokens", "error", err)
	}
}

package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// TooGoodToGo monitors TooGoodToGo favorites for available surprise bags.
type TooGoodToGo struct {
	email   string
	dataDir string // directory for tgtg_tokens.json
	client  *http.Client

	// auth state
	accessToken  string
	refreshToken string
	userID       string
	cookie       string // datadome cookie value
	lastRefresh  time.Time
	pollingID    string // set by StartLogin, consumed by SubmitPIN

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

const (
	tgtgAPKVersion = "26.2.1"
	tgtgUserAgent  = "TGTG/" + tgtgAPKVersion + " Dalvik/2.1.0 (Linux; U; Android 14; Pixel 7 Pro Build/UPSIDE_DOWN_CAKE)"
	tgtgDDK        = "1D42C2CA6131C526E09F294FE96F94"
)

var datadomeCookieRe = regexp.MustCompile(`datadome=([^;]+)`)

func NewTooGoodToGo(email, dataDir string) *TooGoodToGo {
	t := &TooGoodToGo{
		email:       email,
		dataDir:     dataDir,
		client:      &http.Client{Timeout: 30 * time.Second},
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
	if t.accessToken == "" {
		return nil, ErrNeedsLogin
	}

	if time.Since(t.lastRefresh) > 4*time.Hour {
		if err := t.refreshTokens(ctx); err != nil {
			slog.Warn("tgtg token refresh failed, will re-auth next cycle", "error", err)
			t.accessToken = ""
			return nil, nil
		}
	}

	items, err := t.getFavorites(ctx)
	if err != nil {
		return nil, fmt.Errorf("tgtg favorites: %w", err)
	}

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

// NeedsLogin reports whether the plugin requires interactive login.
func (t *TooGoodToGo) NeedsLogin() bool {
	return t.accessToken == ""
}

// StartLogin initiates the email auth flow and stores the polling ID.
// The caller should then prompt the user for the PIN and call SubmitPIN.
func (t *TooGoodToGo) StartLogin(ctx context.Context) error {
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
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("authByEmail: status %d: %s", resp.StatusCode, string(respBody))
	}

	var authResp struct {
		PollingID string `json:"polling_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return fmt.Errorf("authByEmail decode: %w", err)
	}

	t.pollingID = authResp.PollingID
	slog.Info("tgtg: login started, waiting for PIN", "email", t.email, "polling_id", t.pollingID)
	return nil
}

// SubmitPIN completes the auth flow with the PIN from the user.
func (t *TooGoodToGo) SubmitPIN(ctx context.Context, pin string) error {
	if t.pollingID == "" {
		return fmt.Errorf("no login in progress, send /login first")
	}
	if pin == "" {
		return fmt.Errorf("empty PIN")
	}

	slog.Info("tgtg: submitting PIN", "polling_id", t.pollingID)

	pinBody := map[string]any{
		"device_type":        "ANDROID",
		"email":              t.email,
		"request_pin":        pin,
		"request_polling_id": t.pollingID,
	}
	resp, err := t.apiPost(ctx, "/auth/v5/authByRequestPin", pinBody)
	if err != nil {
		t.pollingID = ""
		return fmt.Errorf("authByPin: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.pollingID = ""
		return fmt.Errorf("authByPin: status %d: %s", resp.StatusCode, string(data))
	}

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
		t.pollingID = ""
		return fmt.Errorf("authByPin decode: %w", err)
	}
	t.accessToken = tokenResp.AccessToken
	t.refreshToken = tokenResp.RefreshToken
	t.userID = tokenResp.StartupData.User.UserID
	t.lastRefresh = time.Now()
	t.pollingID = ""
	t.saveTokens()
	slog.Info("tgtg: authenticated successfully")
	return nil
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
	return t.getFavoritesWithRetry(ctx, true)
}

func (t *TooGoodToGo) getFavoritesWithRetry(ctx context.Context, canRetry bool) ([]tgtgItem, error) {
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

	if resp.StatusCode == http.StatusUnauthorized && canRetry {
		resp.Body.Close()
		if err := t.refreshTokens(ctx); err != nil {
			return nil, fmt.Errorf("refresh after 401: %w", err)
		}
		return t.getFavoritesWithRetry(ctx, false)
	}

	if resp.StatusCode == http.StatusForbidden && canRetry {
		resp.Body.Close()
		// 403 = DataDome challenge, refresh cookie and retry
		t.cookie = ""
		if err := t.fetchDataDomeCookie(ctx, t.baseURL+"/item/v8/"); err != nil {
			slog.Warn("tgtg: datadome refresh failed", "error", err)
		}
		return t.getFavoritesWithRetry(ctx, false)
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

// fetchDataDomeCookie obtains a datadome cookie from the DataDome SDK endpoint.
func (t *TooGoodToGo) fetchDataDomeCookie(ctx context.Context, requestURL string) error {
	cid := generateDatadomeCID()
	params := url.Values{
		"camera":   {`{"auth":"true", "info":"{\"front\":\"2000x1500\",\"back\":\"5472x3648\"}"}`},
		"cid":      {cid},
		"ddk":      {tgtgDDK},
		"ddv":      {"3.0.4"},
		"ddvc":     {tgtgAPKVersion},
		"events":   {fmt.Sprintf(`[{"id":1,"message":"response validation","source":"sdk","date":%d}]`, time.Now().UnixMilli())},
		"inte":     {"android-java-okhttp"},
		"mdl":      {"Pixel 7 Pro"},
		"os":       {"Android"},
		"osn":      {"UPSIDE_DOWN_CAKE"},
		"osr":      {"14"},
		"osv":      {"34"},
		"request":  {requestURL},
		"screen_d": {"3.5"},
		"screen_x": {"1440"},
		"screen_y": {"3120"},
		"ua":       {tgtgUserAgent},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", t.datadomeURL,
		strings.NewReader(params.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", tgtgUserAgent)

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var ddResp struct {
		Status int    `json:"status"`
		Cookie string `json:"cookie"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ddResp); err != nil {
		return fmt.Errorf("datadome decode: %w", err)
	}
	if ddResp.Status == 200 && ddResp.Cookie != "" {
		if m := datadomeCookieRe.FindStringSubmatch(ddResp.Cookie); len(m) > 1 {
			t.cookie = m[1]
		}
	}
	return nil
}

func generateDatadomeCID() string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789~_"
	b := make([]byte, 120)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

// apiPost makes a POST request to the TGTG API with DataDome cookie handling.
func (t *TooGoodToGo) apiPost(ctx context.Context, path string, body any) (*http.Response, error) {
	fullURL := t.baseURL + path

	// Ensure datadome cookie before request
	if t.cookie == "" {
		_ = t.fetchDataDomeCookie(ctx, fullURL)
	}

	resp, err := t.doPost(ctx, fullURL, body)
	if err != nil {
		return nil, err
	}

	// On 403, refresh datadome cookie and retry once
	if resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		t.cookie = ""
		if err := t.fetchDataDomeCookie(ctx, fullURL); err != nil {
			return nil, fmt.Errorf("datadome refresh: %w", err)
		}
		return t.doPost(ctx, fullURL, body)
	}

	return resp, nil
}

func (t *TooGoodToGo) doPost(ctx context.Context, url string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-GB")
	req.Header.Set("User-Agent", tgtgUserAgent)
	req.Header.Set("X-App-Version", tgtgAPKVersion)
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
		return
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

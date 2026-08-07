package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"morse/internal/testutil"
)

// favoritesJSON builds a TGTG /item/v8/ response with the given items.
func favoritesJSON(items ...struct {
	ID        string
	Store     string
	Display   string
	Available int
}) string {
	type apiItem struct {
		Item struct {
			ItemID string `json:"item_id"`
		} `json:"item"`
		Store struct {
			StoreName string `json:"store_name"`
		} `json:"store"`
		DisplayName    string `json:"display_name"`
		ItemsAvailable int    `json:"items_available"`
	}
	var out []apiItem
	for _, it := range items {
		ai := apiItem{
			DisplayName:    it.Display,
			ItemsAvailable: it.Available,
		}
		ai.Item.ItemID = it.ID
		ai.Store.StoreName = it.Store
		out = append(out, ai)
	}
	b, _ := json.Marshal(map[string]any{"items": out})
	return string(b)
}

func newTestTGTG(t *testing.T, baseURL string) *TooGoodToGo {
	t.Helper()
	return &TooGoodToGo{
		email:        "test@example.com",
		dataDir:      t.TempDir(),
		client:       &http.Client{Timeout: 5 * time.Second},
		accessToken:  "tok",
		refreshToken: "ref",
		userID:       "u1",
		cookie:       "dd",
		lastRefresh:  time.Now(),
		seen:         make(map[string]bool),
		baseURL:      baseURL,
		datadomeURL:  baseURL + "/datadome",
	}
}

func TestCheckFavoritesAlert(t *testing.T) {
	type item = struct {
		ID        string
		Store     string
		Display   string
		Available int
	}

	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/item/v8/", 200, favoritesJSON(
			item{"1", "Baker", "Bread bag", 3},
			item{"2", "Sushi", "Sushi bag", 0},
		)).
		Start()

	p := newTestTGTG(t, srv.URL())
	alerts, err := p.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("got %d alerts, want 1", len(alerts))
	}
	if alerts[0].Title != "TGTG: Baker" {
		t.Errorf("title = %q", alerts[0].Title)
	}
	if !strings.Contains(alerts[0].Message, "3 bag(s)") {
		t.Errorf("message = %q", alerts[0].Message)
	}
}

func TestSeenResetOnRestock(t *testing.T) {
	type item = struct {
		ID        string
		Store     string
		Display   string
		Available int
	}

	call := 0
	srv := testutil.NewFakeAPI(t).
		HandleFunc("POST", "/item/v8/", func(w http.ResponseWriter, r *http.Request) {
			call++
			switch call {
			case 1:
				w.Write([]byte(favoritesJSON(item{"1", "Baker", "Bread", 2})))
			case 2:
				w.Write([]byte(favoritesJSON(item{"1", "Baker", "Bread", 0})))
			case 3:
				w.Write([]byte(favoritesJSON(item{"1", "Baker", "Bread", 1})))
			}
		}).
		Start()

	p := newTestTGTG(t, srv.URL())

	alerts, _ := p.Check(context.Background())
	if len(alerts) != 1 {
		t.Fatalf("check 1: got %d alerts, want 1", len(alerts))
	}

	alerts, _ = p.Check(context.Background())
	if len(alerts) != 0 {
		t.Fatalf("check 2: got %d alerts, want 0", len(alerts))
	}

	alerts, _ = p.Check(context.Background())
	if len(alerts) != 1 {
		t.Fatalf("check 3: got %d alerts, want 1", len(alerts))
	}
}

func TestRefreshTokens(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/token/v1/refresh", 200,
			`{"access_token":"new_at","refresh_token":"new_rt"}`).
		Start()

	p := newTestTGTG(t, srv.URL())
	if err := p.refreshTokens(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.accessToken != "new_at" {
		t.Errorf("accessToken = %q, want new_at", p.accessToken)
	}
	if p.refreshToken != "new_rt" {
		t.Errorf("refreshToken = %q, want new_rt", p.refreshToken)
	}

	data, err := os.ReadFile(filepath.Join(p.dataDir, "tgtg_tokens.json"))
	if err != nil {
		t.Fatalf("token file not saved: %v", err)
	}
	if !strings.Contains(string(data), "new_at") {
		t.Errorf("saved tokens missing new access token")
	}
}

func TestGetFavoritesRetryOn401(t *testing.T) {
	type item = struct {
		ID        string
		Store     string
		Display   string
		Available int
	}

	var favCalls atomic.Int32

	srv := testutil.NewFakeAPI(t).
		HandleFunc("POST", "/item/v8/", func(w http.ResponseWriter, r *http.Request) {
			n := favCalls.Add(1)
			if n == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Write([]byte(favoritesJSON(item{"1", "Shop", "Bag", 1})))
		}).
		Handle("POST", "/token/v1/refresh", 200,
			`{"access_token":"new_tok","refresh_token":"new_ref"}`).
		Start()

	p := newTestTGTG(t, srv.URL())
	items, err := p.getFavorites(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if favCalls.Load() != 2 {
		t.Errorf("expected 2 favorites calls, got %d", favCalls.Load())
	}
}

func TestGetFavoritesRetryOn403(t *testing.T) {
	type item = struct {
		ID        string
		Store     string
		Display   string
		Available int
	}

	var favCalls atomic.Int32

	srv := testutil.NewFakeAPI(t).
		HandleFunc("POST", "/item/v8/", func(w http.ResponseWriter, r *http.Request) {
			n := favCalls.Add(1)
			if n == 1 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Write([]byte(favoritesJSON(item{"1", "Shop", "Bag", 1})))
		}).
		Handle("POST", "/datadome", 200, `{"status":200,"cookie":"datadome=newdd; Path=/; Secure"}`).
		Start()

	p := newTestTGTG(t, srv.URL())
	items, err := p.getFavorites(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if p.cookie != "newdd" {
		t.Errorf("cookie = %q, want newdd", p.cookie)
	}
}

func TestLoadSaveTokens(t *testing.T) {
	dir := t.TempDir()

	p1 := &TooGoodToGo{
		dataDir:      dir,
		accessToken:  "at1",
		refreshToken: "rt1",
		userID:       "uid1",
		cookie:       "ck1",
		lastRefresh:  time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC),
	}
	p1.saveTokens()

	p2 := &TooGoodToGo{dataDir: dir, seen: make(map[string]bool)}
	p2.loadTokens()

	if p2.accessToken != "at1" {
		t.Errorf("accessToken = %q, want at1", p2.accessToken)
	}
	if p2.refreshToken != "rt1" {
		t.Errorf("refreshToken = %q, want rt1", p2.refreshToken)
	}
	if p2.userID != "uid1" {
		t.Errorf("userID = %q, want uid1", p2.userID)
	}
	if p2.cookie != "ck1" {
		t.Errorf("cookie = %q, want ck1", p2.cookie)
	}
}

func TestTGTGDescribe(t *testing.T) {
	p := NewTooGoodToGo("user@example.com", t.TempDir())
	desc := p.Describe()
	if !strings.Contains(desc, "Email: user@example.com") {
		t.Errorf("Describe() missing email, got:\n%s", desc)
	}
	if !strings.Contains(desc, "Mode: favorites") {
		t.Errorf("Describe() missing mode, got:\n%s", desc)
	}
}

func TestNeedsLogin(t *testing.T) {
	p := &TooGoodToGo{seen: make(map[string]bool)}
	if !p.NeedsLogin() {
		t.Error("NeedsLogin() = false, want true when no access token")
	}
	p.accessToken = "tok"
	if p.NeedsLogin() {
		t.Error("NeedsLogin() = true, want false when access token is set")
	}
}

func TestCheckReturnsErrNeedsLogin(t *testing.T) {
	p := &TooGoodToGo{seen: make(map[string]bool)}
	_, err := p.Check(context.Background())
	if err != ErrNeedsLogin {
		t.Errorf("Check() error = %v, want ErrNeedsLogin", err)
	}
}

func TestStartLogin(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/auth/v5/authByEmail", 200, `{"polling_id":"poll123"}`).
		Handle("POST", "/datadome", 200, `{"status":200,"cookie":"datadome=dd1; Path=/; Secure"}`).
		Start()

	p := &TooGoodToGo{
		email:       "test@example.com",
		dataDir:     t.TempDir(),
		client:      &http.Client{Timeout: 5 * time.Second},
		seen:        make(map[string]bool),
		baseURL:     srv.URL(),
		datadomeURL: srv.URL() + "/datadome",
	}

	if err := p.StartLogin(context.Background()); err != nil {
		t.Fatalf("StartLogin() error: %v", err)
	}
	if p.pollingID != "poll123" {
		t.Errorf("pollingID = %q, want poll123", p.pollingID)
	}
}

func TestSubmitPIN(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/auth/v5/authByRequestPin", 200,
			`{"access_token":"at1","refresh_token":"rt1","startup_data":{"user":{"user_id":"uid1"}}}`).
		Handle("POST", "/datadome", 200, `{"status":200,"cookie":"datadome=dd1; Path=/; Secure"}`).
		Start()

	p := &TooGoodToGo{
		email:       "test@example.com",
		dataDir:     t.TempDir(),
		client:      &http.Client{Timeout: 5 * time.Second},
		seen:        make(map[string]bool),
		baseURL:     srv.URL(),
		datadomeURL: srv.URL() + "/datadome",
		pollingID:   "poll123",
	}

	if err := p.SubmitPIN(context.Background(), "664047"); err != nil {
		t.Fatalf("SubmitPIN() error: %v", err)
	}
	if p.accessToken != "at1" {
		t.Errorf("accessToken = %q, want at1", p.accessToken)
	}
	if p.refreshToken != "rt1" {
		t.Errorf("refreshToken = %q, want rt1", p.refreshToken)
	}
	if p.userID != "uid1" {
		t.Errorf("userID = %q, want uid1", p.userID)
	}
	if p.pollingID != "" {
		t.Errorf("pollingID = %q, want empty after successful PIN", p.pollingID)
	}
	if p.NeedsLogin() {
		t.Error("NeedsLogin() = true after successful SubmitPIN")
	}

	// Verify tokens were saved to disk
	data, err := os.ReadFile(filepath.Join(p.dataDir, "tgtg_tokens.json"))
	if err != nil {
		t.Fatalf("token file not saved: %v", err)
	}
	if !strings.Contains(string(data), "at1") {
		t.Errorf("saved tokens missing access token")
	}
}

func TestSubmitPINWithoutLogin(t *testing.T) {
	p := &TooGoodToGo{seen: make(map[string]bool)}
	err := p.SubmitPIN(context.Background(), "123456")
	if err == nil {
		t.Fatal("SubmitPIN() without StartLogin should fail")
	}
	if !strings.Contains(err.Error(), "no login in progress") {
		t.Errorf("error = %q, want 'no login in progress'", err.Error())
	}
}

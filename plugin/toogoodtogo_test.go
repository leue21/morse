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

	"salert/internal/testutil"
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
		userAgent:    "test",
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
			case 1: // available
				w.Write([]byte(favoritesJSON(item{"1", "Baker", "Bread", 2})))
			case 2: // sold out
				w.Write([]byte(favoritesJSON(item{"1", "Baker", "Bread", 0})))
			case 3: // restocked
				w.Write([]byte(favoritesJSON(item{"1", "Baker", "Bread", 1})))
			}
		}).
		Start()

	p := newTestTGTG(t, srv.URL())

	// Check 1: should alert
	alerts, _ := p.Check(context.Background())
	if len(alerts) != 1 {
		t.Fatalf("check 1: got %d alerts, want 1", len(alerts))
	}

	// Check 2: sold out, no alert, seen should be cleared
	alerts, _ = p.Check(context.Background())
	if len(alerts) != 0 {
		t.Fatalf("check 2: got %d alerts, want 0", len(alerts))
	}

	// Check 3: restocked, should alert again
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

	// Verify tokens were saved
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
		Handle("POST", "/datadome", 200, `{"cookie":"new_dd"}`).
		Start()

	p := newTestTGTG(t, srv.URL())
	items, err := p.getFavorites(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if p.cookie != "new_dd" {
		t.Errorf("cookie = %q, want new_dd", p.cookie)
	}
}

func TestLoadSaveTokens(t *testing.T) {
	dir := t.TempDir()

	// Create and save
	p1 := &TooGoodToGo{
		dataDir:      dir,
		accessToken:  "at1",
		refreshToken: "rt1",
		userID:       "uid1",
		cookie:       "ck1",
		lastRefresh:  time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC),
	}
	p1.saveTokens()

	// Load into new instance
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

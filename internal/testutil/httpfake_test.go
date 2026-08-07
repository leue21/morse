package testutil

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFakeAPIMatchesRoute(t *testing.T) {
	srv := NewFakeAPI(t).
		Handle("GET", "/hello", 200, `{"msg":"hi"}`).
		Start()

	resp, err := http.Get(srv.URL() + "/hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"msg":"hi"}` {
		t.Errorf("body = %q", string(body))
	}
}

func TestFakeAPI404ForUnknownRoute(t *testing.T) {
	srv := NewFakeAPI(t).
		Handle("GET", "/known", 200, "ok").
		Start()

	resp, err := http.Get(srv.URL() + "/unknown")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestFakeAPIRecordsRequests(t *testing.T) {
	srv := NewFakeAPI(t).
		Handle("POST", "/data", 201, "created").
		Start()

	http.Post(srv.URL()+"/data", "application/json", strings.NewReader(`{"key":"val"}`))

	if len(srv.Requests) != 1 {
		t.Fatalf("got %d requests, want 1", len(srv.Requests))
	}
	req := srv.Requests[0]
	if req.Method != "POST" {
		t.Errorf("method = %q, want POST", req.Method)
	}
	if req.Path != "/data" {
		t.Errorf("path = %q, want /data", req.Path)
	}
	if string(req.Body) != `{"key":"val"}` {
		t.Errorf("body = %q", string(req.Body))
	}
}

func TestFakeAPIMethodMismatch(t *testing.T) {
	srv := NewFakeAPI(t).
		Handle("POST", "/only-post", 200, "ok").
		Start()

	resp, err := http.Get(srv.URL() + "/only-post")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404 for method mismatch", resp.StatusCode)
	}
}

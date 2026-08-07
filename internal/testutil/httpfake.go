package testutil

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Request records a request received by FakeAPI. The headers come along
// because the body alone is not always readable without them: a multipart
// upload can only be parsed with the boundary from its Content-Type.
type Request struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

type route struct {
	method  string
	path    string
	handler http.HandlerFunc
}

// FakeAPI is a lightweight test HTTP server with route matching.
type FakeAPI struct {
	t      testing.TB
	routes []route
	Server *httptest.Server

	mu       sync.Mutex
	Requests []Request
}

// NewFakeAPI creates a new FakeAPI bound to the given test.
func NewFakeAPI(t testing.TB) *FakeAPI {
	t.Helper()
	return &FakeAPI{t: t}
}

// Handle registers a route that returns the given status and body.
func (f *FakeAPI) Handle(method, path string, status int, body string) *FakeAPI {
	return f.HandleFunc(method, path, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		io.WriteString(w, body)
	})
}

// HandleFunc registers a route with a custom handler function.
func (f *FakeAPI) HandleFunc(method, path string, handler http.HandlerFunc) *FakeAPI {
	f.routes = append(f.routes, route{method: method, path: path, handler: handler})
	return f
}

// Start creates and starts the underlying httptest.Server.
// The server is automatically closed when the test finishes.
func (f *FakeAPI) Start() *FakeAPI {
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.Requests = append(f.Requests, Request{
			Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body,
		})
		f.mu.Unlock()

		for _, rt := range f.routes {
			if rt.method == r.Method && rt.path == r.URL.Path {
				rt.handler(w, r)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	f.t.Cleanup(f.Server.Close)
	return f
}

// URL returns the base URL of the running server.
func (f *FakeAPI) URL() string {
	return f.Server.URL
}

package routeros

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func strconvAtou(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("invalid port: %v", err)
	}
	return n
}

func newTestRouterOS(t *testing.T, handler http.HandlerFunc) *RouterOS {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	u, _ := url.Parse(srv.URL)
	return &RouterOS{
		cfg:    &Config{},
		client: srv.Client(),
		logger: slog.Default(),
		url:    u,
	}
}

func TestNew_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	cfg := &Config{
		Host:     u.Hostname(),
		Port:     strconvAtou(t, u.Port()),
		User:     "admin",
		Password: "pass",
	}
	ros, err := New(cfg, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ros == nil {
		t.Fatal("expected non-nil RouterOS")
	}
}

func TestNew_VerifyConnectionFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	cfg := &Config{
		Host:     u.Hostname(),
		Port:     strconvAtou(t, u.Port()),
		User:     "admin",
		Password: "wrong",
	}
	_, err := New(cfg, slog.Default())
	if err == nil {
		t.Fatal("expected error from failed connection verification")
	}
}

func TestNew_InvalidHost(t *testing.T) {
	cfg := &Config{
		Host:     "nonexistent.invalid",
		Port:     12345,
		User:     "admin",
		Password: "pass",
	}
	_, err := New(cfg, slog.Default())
	if err == nil {
		t.Fatal("expected error for invalid host")
	}
}

func TestVerifyConnection_OK(t *testing.T) {
	ros := newTestRouterOS(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/system/resource" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})

	err := ros.verifyConnection(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifyConnection_ServerError(t *testing.T) {
	ros := newTestRouterOS(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	err := ros.verifyConnection(context.Background())
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestGet_SendsBasicAuth(t *testing.T) {
	ros := newTestRouterOS(t, func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok {
			t.Error("expected basic auth header")
		}
		if user != "admin" || pass != "secret" {
			t.Errorf("unexpected credentials: %q/%q", user, pass)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"model":"RB4011"}`))
	})
	ros.cfg = &Config{User: "admin", Password: "secret"}

	out, err := ros.get[map[string]any](context.Background(), "/system/resource")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if (*out)["model"] != "RB4011" {
		t.Errorf("unexpected response: %v", *out)
	}
}

func TestGet_BadJSON(t *testing.T) {
	ros := newTestRouterOS(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not-json`))
	})

	_, err := ros.get[map[string]any](context.Background(), "/system/resource")
	if err == nil {
		t.Fatal("expected error for invalid json")
	}
}

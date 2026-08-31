package netutil

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewHTTPClientIgnoresEnvironmentProxy(t *testing.T) {
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := NewHTTPClient("")
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("GET through direct client: %v", err)
	}
	resp.Body.Close()
}

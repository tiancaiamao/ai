package netutil

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewHTTPClientIgnoresEnvironmentProxy(t *testing.T) {
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

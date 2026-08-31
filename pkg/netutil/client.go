// Package netutil provides HTTP clients with explicit proxy behavior.
package netutil

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/net/proxy"
)

// NewHTTPClient returns an HTTP client that uses proxyURL when non-empty.
// An empty proxyURL means direct connections; process proxy environment
// variables are intentionally ignored.
func NewHTTPClient(proxyURL string) (*http.Client, error) {
	client := &http.Client{}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return client, nil
	}
	transport = transport.Clone()
	transport.Proxy = nil

	if strings.TrimSpace(proxyURL) == "" {
		client.Transport = transport
		return client, nil
	}

	parsed, err := parseProxyURL(proxyURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "socks5" || parsed.Scheme == "socks5h" {
		dialer, err := proxy.SOCKS5("tcp", parsed.Host, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("create SOCKS5 proxy: %w", err)
		}
		if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
			transport.DialContext = contextDialer.DialContext
		} else {
			transport.Dial = dialer.Dial
		}
	} else if parsed.Scheme == "http" || parsed.Scheme == "https" {
		transport.Proxy = http.ProxyURL(parsed)
	} else {
		return nil, fmt.Errorf("unsupported proxy scheme %q", parsed.Scheme)
	}

	client.Transport = transport
	return client, nil
}

// NewEnvironmentHTTPClient returns a client using standard proxy variables.
// It is intended for standalone authentication flows, not model requests.
func NewEnvironmentHTTPClient() (*http.Client, error) {
	for _, key := range []string{"ALL_PROXY", "HTTPS_PROXY", "HTTP_PROXY"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return NewHTTPClient(value)
		}
	}
	return NewHTTPClient("")
}

func parseProxyURL(value string) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("proxy URL is empty")
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	return url.Parse(value)
}

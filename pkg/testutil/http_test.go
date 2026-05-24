package testutil

import (
	"io"
	"net/http"
)

func httpGet(url string) (int, error) {
	resp, err := http.Get(url) //nolint:gosec // test helper
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

func httpGetFull(url string) (string, error) {
	resp, err := http.Get(url) //nolint:gosec // test helper
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

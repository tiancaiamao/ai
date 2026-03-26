package httpfile

import (
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"strings"
)

// Transforms request
//
//	Authentification: Basic abcd efgh
//
// to
//
//	Authentification: Basic Base64(abcd:efgh)
func AuthToBase64(header HTTPHeader) HTTPHeader {
	if header.Key != "Authorization" {
		return header
	}
	if !strings.HasPrefix(header.Value, "Basic") {
		return header
	}
	header.Value = strings.TrimPrefix(header.Value, "Basic")
	header.Value = strings.TrimSpace(header.Value)
	//replace multiple spaces
	space := regexp.MustCompile(`\s+`)
	header.Value = space.ReplaceAllString(header.Value, " ")
	userPass := strings.Split(header.Value, " ")

	if len(userPass) != 2 { // Probably already base 64 encoded
		header.Value = "Basic " + userPass[0]
		return header
	}
	header.Value = "Basic " + base64.StdEncoding.EncodeToString([]byte(userPass[0]+":"+userPass[1]))
	return header
}

func PrepareRequest(r HTTPFile, addKeepAlive bool) (*http.Request, error) {
	req, err := http.NewRequest(r.Method, r.URL, strings.NewReader(r.Body))
	if err != nil {
		return nil, errors.New("failed to create HTTP request: " + err.Error())
	}
	q := req.URL.Query()
	for _, p := range r.Parameter {
		q.Add(p.Key, p.Value)
	}
	req.URL.RawQuery = q.Encode()

	for _, h := range r.Header {
		h = AuthToBase64(h)
		if h.Key == "Host" {
			req.Host = h.Value
		}
		req.Header.Set(h.Key, h.Value)
	}
	if addKeepAlive {
		req.Header.Set("Connection", "keep-alive")
	}
	return req, nil
}

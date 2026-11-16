//go:build wordpress_offline_lab

package offline

import (
	"bytes"
	"errors"
	"io"
	"net/http"
)

// Transport resolves exact fixture requests without a network fallback.
type Transport struct{}

func (Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil || req.Method != http.MethodGet || req.Body != nil || req.URL.User != nil || req.URL.Fragment != "" {
		return nil, errors.New("offline: invalid request")
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	b, err := Resolve(req.URL.String())
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(b)), ContentLength: int64(len(b)), Request: req}, nil
}

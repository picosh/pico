package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOriginCheckFailures(t *testing.T) {
	for _, mode := range []string{"network", "oversized", "redirect", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int32
			client := &http.Client{
				CheckRedirect: originCheckClient.CheckRedirect,
				Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					requests.Add(1)
					if req.URL.Host != "ash.pgs.sh" || req.Host != "pgs.sh" || req.URL.Path != "/check" {
						t.Errorf("unexpected target: %s, host %s", req.URL, req.Host)
					}
					if mode == "network" {
						return nil, errors.New("origin unavailable")
					}
					if mode == "canceled" {
						<-req.Context().Done()
						return nil, req.Context().Err()
					}
					status, body := http.StatusOK, "ok"
					headers := http.Header{}
					if mode == "oversized" {
						body = strings.Repeat("x", 64*1024+1)
					} else {
						status = http.StatusFound
						headers.Set("Location", "http://127.0.0.1/private")
					}
					return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body))}, nil
				}),
			}
			handler := &cachedHttp{Logger: slog.New(slog.DiscardHandler), CheckClient: client}
			req := httptest.NewRequest(http.MethodGet, "https://pgs.sh/check?domain=example.com", nil)
			if mode == "canceled" {
				ctx, cancel := context.WithCancel(req.Context())
				cancel()
				req = req.WithContext(ctx)
			}
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusBadGateway || requests.Load() > 1 {
				t.Fatalf("status %d with %d requests: %s", res.Code, requests.Load(), res.Body)
			}
		})
	}
}

func TestOriginCheckPreservesStatus(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNotFound} {
		handler := &cachedHttp{
			Logger: slog.New(slog.DiscardHandler),
			CheckClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.RawQuery != "domain=example.com" {
					t.Errorf("query changed: %s", req.URL.RawQuery)
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("checked"))}, nil
			})},
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "https://pgs.sh/check?domain=example.com", nil))
		if res.Code != status || res.Body.String() != "checked" {
			t.Fatalf("status %d: %s", res.Code, res.Body)
		}
	}
}

func TestOriginProxyPinsTargetAndPreservesPath(t *testing.T) {
	proxy := newProxyServe(slog.New(slog.DiscardHandler))
	proxy.transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://ash.pgs.sh/encoded%2Fpath?x=1" || req.Host != "site.example" || req.Header.Get("Accept-Encoding") != "" {
			t.Errorf("unexpected request: %s, host %s, headers %v", req.URL, req.Host, req.Header)
		}
		if _, ok := req.Context().Deadline(); !ok {
			t.Error("origin proxy has no deadline")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/plain"}}, Body: io.NopCloser(strings.NewReader("asset"))}, nil
	})
	req := httptest.NewRequest(http.MethodGet, "https://site.example/encoded%2Fpath?x=1", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res := httptest.NewRecorder()
	proxy.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "asset" {
		t.Fatalf("status %d: %s", res.Code, res.Body)
	}
}

package tui

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchTunnelStatus(t *testing.T) {
	const secret = "fixture&token=value"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("x-authorization") != secret {
			t.Errorf("authorization query was not preserved")
		}
		if r.URL.Path != "/_sish/api/clients" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		switch r.Header.Get("X-Fixture") {
		case "status":
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "private response body")
		case "large":
			_, _ = io.WriteString(w, strings.Repeat(" ", maxTunsResponseSize+1))
		case "redirect":
			http.Redirect(w, r, "http://127.0.0.1/private", http.StatusFound)
		case "slow":
			<-r.Context().Done()
		default:
			_, _ = io.WriteString(w, `{"status":true,"clients":{"fixture":{"user":"tester"}}}`)
		}
	}))
	defer server.Close()
	baseTransport := server.Client().Transport
	for _, mode := range []string{"success", "status", "large", "redirect", "slow"} {
		t.Run(mode, func(t *testing.T) {
			client := *server.Client()
			client.Timeout = 2 * time.Second
			if mode == "slow" {
				client.Timeout = 100 * time.Millisecond
			}
			client.CheckRedirect = tunsHTTPClient.CheckRedirect
			client.Transport = tunsTestTransport{base: baseTransport, mode: mode}
			data, err := fetch(t.Context(), &client, server.Listener.Addr().String(), secret)
			if mode == "success" {
				if err != nil || data["fixture"].User != "tester" {
					t.Fatalf("data %v: %v", data, err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			want := map[string]string{"status": "403", "large": "byte limit", "redirect": "302"}[mode]
			if want != "" && !strings.Contains(err.Error(), want) {
				t.Errorf("%v, want %s", err, want)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "private response body") {
				t.Fatalf("error exposed sensitive details: %v", err)
			}
			if mode == "slow" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected timeout, got %v", err)
			}
		})
	}
}

type tunsTestTransport struct {
	base http.RoundTripper
	mode string
}

func (t tunsTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("X-Fixture", t.mode)
	return t.base.RoundTrip(req)
}

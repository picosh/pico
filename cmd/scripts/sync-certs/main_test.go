package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSplitDirectoryKey(t *testing.T) {
	tests := []struct {
		key       string
		baseIsDir bool
		wantDir   string
		wantBase  string
	}{
		{
			key:       "caddy/certificates/acme.org/site.com/site.com.crt",
			baseIsDir: false,
			wantDir:   "caddy/certificates/acme.org/site.com",
			wantBase:  "site.com.crt",
		},
		{
			key:       "caddy/certificates/acme.org/site.com",
			baseIsDir: true,
			wantDir:   "caddy/certificates/acme.org",
			wantBase:  "site.com/",
		},
		{
			key:       "caddy",
			baseIsDir: true,
			wantDir:   ".",
			wantBase:  "caddy/",
		},
	}

	for _, tt := range tests {
		gotDir, gotBase := splitDirectoryKey(tt.key, tt.baseIsDir)
		if gotDir != tt.wantDir || gotBase != tt.wantBase {
			t.Errorf("splitDirectoryKey(%q, %v) = (%q, %q), want (%q, %q)",
				tt.key, tt.baseIsDir, gotDir, gotBase, tt.wantDir, tt.wantBase)
		}
	}
}

func TestStorageDataJSON(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	sd := StorageData{
		Value:       []byte("test-cert-data"),
		Modified:    now,
		Size:        14,
		Compression: 0,
		Encryption:  0,
	}

	data, err := json.Marshal(sd)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	// In Go json.Marshal, []byte encodes as base64 string
	if val, ok := parsed["value"].(string); !ok || val == "" {
		t.Errorf("expected base64 string for value, got: %v", parsed["value"])
	}
	if size, ok := parsed["size"].(float64); !ok || int64(size) != 14 {
		t.Errorf("expected size 14, got: %v", parsed["size"])
	}
}

func TestFindCaddyRoots(t *testing.T) {
	tmp := t.TempDir()

	// 1. Root with certificates/
	caddy1 := filepath.Join(tmp, "caddy1")
	if err := os.MkdirAll(filepath.Join(caddy1, "certificates"), 0755); err != nil {
		t.Fatal(err)
	}

	// 2. Nested with data/caddy/acme/
	service2 := filepath.Join(tmp, "service2", "data", "caddy")
	if err := os.MkdirAll(filepath.Join(service2, "acme"), 0755); err != nil {
		t.Fatal(err)
	}

	roots := findCaddyRoots(tmp)
	if len(roots) != 2 {
		t.Errorf("expected 2 roots, got %d: %v", len(roots), roots)
	}
}

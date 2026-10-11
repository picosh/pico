package mime

import "testing"

func TestGetMimeType(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "binary", path: "archive.bin", want: "application/octet-stream"},
		{name: "binary_extension_is_case_insensitive", path: "archive.BIN", want: "application/octet-stream"},
		{name: "unknown_extension_fallback", path: "archive.unknown", want: "text/plain"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetMimeType(tt.path); got != tt.want {
				t.Errorf("GetMimeType(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

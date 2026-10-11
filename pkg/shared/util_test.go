package shared

import "testing"

func TestStorageUsage(t *testing.T) {
	tests := []struct {
		used, max int
		want      string
	}{
		{used: 0, max: 1000 * MB, want: ""},
		{used: 999 * MB, max: 1000 * MB, want: ""},
		{used: 1000 * MB, max: 1000 * MB, want: " (space: 1.00/1.00GB, 100.00%)"},
		{used: 10, max: 0, want: ""},
	}
	for _, tt := range tests {
		if got := StorageUsage(tt.used, tt.max); got != tt.want {
			t.Errorf("StorageUsage(%d, %d) = %q, want %q", tt.used, tt.max, got, tt.want)
		}
	}
}

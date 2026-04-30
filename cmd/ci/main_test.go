package main

import (
	"testing"
)

func TestGenerateJobID(t *testing.T) {
	// Same inputs should produce same hash
	id1 := jobIDFor("myrepo", "/workspace", 1000)
	id2 := jobIDFor("myrepo", "/workspace", 1000)
	if id1 != id2 {
		t.Errorf("expected same ID for same inputs, got %q and %q", id1, id2)
	}

	// Different name should produce different hash
	id3 := jobIDFor("otherrepo", "/workspace", 1000)
	if id1 == id3 {
		t.Errorf("expected different IDs for different names, got %q", id1)
	}

	// Different timestamp should produce different hash
	id4 := jobIDFor("myrepo", "/workspace", 2000)
	if id1 == id4 {
		t.Errorf("expected different IDs for different timestamps, got %q", id1)
	}

	// ID should be 8 hex chars
	if len(id1) != 8 {
		t.Errorf("expected 8 char ID, got %d chars: %q", len(id1), id1)
	}

	// generateJobID (with real time) should also produce valid IDs
	id := generateJobID("myrepo", "/workspace")
	if len(id) != 8 {
		t.Errorf("generateJobID expected 8 char ID, got %d chars: %q", len(id), id)
	}
}

func TestShortSessionName(t *testing.T) {
	tests := []struct {
		session string
		prefix  string
		want    string
	}{
		{"myrepo.abc123.lint", "myrepo.abc123.", "lint"},
		{"myrepo.abc123.tests", "myrepo.abc123.", "tests"},
		{"lint", "myrepo.abc123.", "lint"}, // no prefix match
	}

	for _, tt := range tests {
		got := shortSessionName(tt.session, tt.prefix)
		if got != tt.want {
			t.Errorf("shortSessionName(%q, %q) = %q, want %q", tt.session, tt.prefix, got, tt.want)
		}
	}
}

func TestAllCompleted(t *testing.T) {
	tests := []struct {
		name     string
		sessions []SessionInfo
		want     bool
	}{
		{
			name:     "empty sessions",
			sessions: []SessionInfo{},
			want:     true,
		},
		{
			name: "all completed",
			sessions: []SessionInfo{
				{Name: "a", Ended: "123"},
				{Name: "b", Ended: "456"},
			},
			want: true,
		},
		{
			name: "one not completed",
			sessions: []SessionInfo{
				{Name: "a", Ended: "123"},
				{Name: "b", Ended: ""},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := allCompleted(tt.sessions)
			if got != tt.want {
				t.Errorf("allCompleted() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseZMXList(t *testing.T) {
	output := `name=ci-lint	pid=1064464	clients=0	created=1777519944	start_dir=/home/erock/dev/pico	ended=1777519986	exit_code=0
  name=ci-tests	pid=1064472	clients=0	created=1777519944	start_dir=/home/erock/dev/pico	ended=1777519958	exit_code=2
→ name=d.build.1	pid=549652	clients=0	created=1777513430	start_dir=/home/erock`

	sessions := parseZMXList(output)
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}

	if sessions[0].Name != "ci-lint" {
		t.Errorf("expected first session name ci-lint, got %q", sessions[0].Name)
	}
	if sessions[0].Ended != "1777519986" {
		t.Errorf("expected ended 1777519986, got %q", sessions[0].Ended)
	}
	if sessions[0].ExitCode != "0" {
		t.Errorf("expected exit_code 0, got %q", sessions[0].ExitCode)
	}

	if sessions[2].Name != "d.build.1" {
		t.Errorf("expected third session name d.build.1, got %q", sessions[2].Name)
	}
	if sessions[2].Ended != "" {
		t.Errorf("expected empty ended for active session, got %q", sessions[2].Ended)
	}
}

func TestFilterSessions(t *testing.T) {
	sessions := []SessionInfo{
		{Name: "myrepo.abc123.lint", PID: "1"},
		{Name: "myrepo.abc123.tests", PID: "2"},
		{Name: "other.xyz.lint", PID: "3"},
	}

	filtered := filterSessions(sessions, "myrepo.abc123.")
	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered sessions, got %d", len(filtered))
	}

	if filtered[0].Short != "lint" {
		t.Errorf("expected short name lint, got %q", filtered[0].Short)
	}
	if filtered[1].Short != "tests" {
		t.Errorf("expected short name tests, got %q", filtered[1].Short)
	}
}

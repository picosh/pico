package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestE2E_RunnerWithZMXSessions is a full integration test that:
// 1. Creates a workspace with pico.sh that spawns zmx sessions
// 2. Feeds an event to RunRunner via a pipe
// 3. Runs the full lifecycle: setup → run → monitor → complete
// 4. Reads the status file and asserts correct status transitions.
func TestE2E_RunnerWithZMXSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test")
	}

	// 1. Create workspace with pico.sh that spawns zmx sessions
	workspaceDir := t.TempDir()
	picoSh := `#!/usr/bin/env bash
set -e
zmx run step1 echo "hello from step1"
zmx run step2 echo "hello from step2"
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "pico.sh"), []byte(picoSh), 0755); err != nil {
		t.Fatalf("write pico.sh: %v", err)
	}

	// 2. Create status file
	statusFile := filepath.Join(t.TempDir(), "status.jsonl")

	// 3. Create config
	ctx, cancel := context.WithCancel(context.Background())
	cfg := &Cfg{
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		Ctx:             ctx,
		Cancel:          cancel,
		ArtifactDir:     t.TempDir(),
		EventsFile:      "", // pipe mode
		StatusFile:      statusFile,
		MonitorInterval: 200 * time.Millisecond,
		NewWorkspace:    defaultWorkspaceFactory,
	}

	// Write event to file before starting the runner
	eventFile := filepath.Join(t.TempDir(), "events.jsonl")
	event := Event{
		Type:      "build",
		Name:      "test-repo",
		Workspace: workspaceDir,
	}
	eventJSON, _ := json.Marshal(event)
	if err := os.WriteFile(eventFile, append(eventJSON, '\n'), 0644); err != nil {
		t.Fatalf("write event: %v", err)
	}
	cfg.EventsFile = eventFile

	// 5. Start runner in goroutine
	done := make(chan error, 1)
	go func() {
		done <- RunRunner(cfg)
	}()

	// 7. Wait for runner to complete (with timeout)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runner: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for runner to complete")
	}

	// 8. Read and parse status file
	data, err := os.ReadFile(statusFile)
	if err != nil {
		t.Fatalf("read status file: %v", err)
	}

	lines := scanLines(data)
	if len(lines) == 0 {
		t.Fatal("no status lines written")
	}

	// Parse all status payloads
	var payloads []StatusPayload
	for _, line := range lines {
		var p StatusPayload
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("unmarshal status line %q: %v", line, err)
		}
		payloads = append(payloads, p)
	}

	// 9. Assert we got at least one "running" and one final payload
	var hasRunning, hasFinal bool
	var finalPayload *StatusPayload
	for i := range payloads {
		p := &payloads[i]
		if p.Status == "running" {
			hasRunning = true
		}
		if p.Status == "success" || p.Status == "failed" {
			hasFinal = true
			finalPayload = p
		}
	}

	if !hasRunning {
		t.Error("expected at least one 'running' status")
	}
	if !hasFinal {
		t.Error("expected a final 'success' or 'failed' status")
	}

	// 10. Assert final payload has correct data
	if finalPayload == nil {
		t.Fatal("no final payload")
	}
	if finalPayload.Name != "test-repo" {
		t.Errorf("expected name test-repo, got %q", finalPayload.Name)
	}
	if finalPayload.Status != "success" {
		t.Errorf("expected status success, got %q", finalPayload.Status)
	}
	if finalPayload.ExitCode == nil || *finalPayload.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %v", finalPayload.ExitCode)
	}
	if len(finalPayload.Sessions) < 2 {
		t.Errorf("expected at least 2 sessions, got %d", len(finalPayload.Sessions))
	}

	// 11. Assert sessions have correct names
	sessionNames := make(map[string]bool)
	for _, s := range finalPayload.Sessions {
		sessionNames[s.Short] = true
		t.Logf("session: name=%s short=%s exit_code=%s ended=%s", s.Name, s.Short, s.ExitCode, s.Ended)
	}
	if !sessionNames["step1"] {
		t.Error("expected session 'step1'")
	}
	if !sessionNames["step2"] {
		t.Error("expected session 'step2'")
	}

	// 12. Assert HTML artifacts were staged for each session
	for _, s := range finalPayload.Sessions {
		artifactPath := filepath.Join(cfg.ArtifactDir, finalPayload.Name, finalPayload.JobID, s.Short+".html")
		data, err := os.ReadFile(artifactPath)
		if err != nil {
			t.Errorf("read artifact %s: %v", artifactPath, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("artifact %s is empty", artifactPath)
		}
		if !bytes.Contains(data, []byte("<div")) {
			t.Errorf("artifact %s does not contain HTML content", artifactPath)
		}
	}

	// Cleanup any leftover zmx sessions
	_ = exec.Command("zmx", "kill", "-f", "test-repo").Run()
}

func scanLines(data []byte) []string {
	var lines []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

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

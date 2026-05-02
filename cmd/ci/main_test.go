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
	"strings"
	"testing"
	"time"
)

// TestE2E_RunnerWithZMXSessions is a full integration test that:
// 1. Creates a workspace with pico.sh that spawns zmx sessions
// 2. Feeds an event to RunRunner (fire-and-forget)
// 3. Runs the monitor to track job completion
// 4. Reads the status file and asserts correct status transitions.
func TestE2E_RunnerWithZMXSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test")
	}
	if _, err := exec.LookPath("zmx"); err != nil {
		t.Skip("zmx not found, skipping integration test")
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

	// 2. Create config
	artifactDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	event := Event{
		Type:      "build",
		Name:      "test-repo",
		Workspace: workspaceDir,
	}
	eventJSON, _ := json.Marshal(event)
	cfg := &Cfg{
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		Ctx:             ctx,
		Cancel:          cancel,
		ArtifactDir:     artifactDir,
		EventSource:     io.NopCloser(bytes.NewReader(append(eventJSON, '\n'))),
		MonitorInterval: 200 * time.Millisecond,
		NewWorkspace:    defaultWorkspaceFactory,
	}

	// 3. Run the runner (fire-and-forget, exits quickly)
	runnerDone := make(chan error, 1)
	go func() {
		runnerDone <- RunRunner(cfg)
	}()

	select {
	case err := <-runnerDone:
		if err != nil {
			t.Fatalf("runner: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for runner to complete")
	}

	// 4. Run the monitor until we see a final status
	monitorDone := make(chan error, 1)
	go func() {
		monitorDone <- runMonitor(cfg)
	}()

	// 5. Wait for final status (with timeout)
	var finalPayload *StatusPayload
	for {
		select {
		case err := <-monitorDone:
			t.Fatalf("monitor exited unexpectedly: %v", err)
		case <-time.After(30 * time.Second):
			t.Fatal("timeout waiting for final status from monitor")
		default:
		}

		// Check status file for final payload
		statusFile := filepath.Join(artifactDir, "status.jsonl")
		data, err := os.ReadFile(statusFile)
		if err == nil {
			lines := scanLines(data)
			for _, line := range lines {
				var p StatusPayload
				if err := json.Unmarshal([]byte(line), &p); err != nil {
					continue
				}
				if p.Status == "success" || p.Status == "failed" {
					finalPayload = &p
				}
			}
		}

		if finalPayload != nil {
			cancel() // stop the monitor
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Wait for monitor to exit gracefully
	select {
	case <-monitorDone:
	case <-time.After(5 * time.Second):
		t.Log("warning: monitor did not exit gracefully")
	}

	// 6. Assert final payload has correct data
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

	// 7. Assert sessions have correct names
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

	// 8. Assert HTML artifacts were staged for each session
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
	_ = exec.Command("zmx", "kill", "-f", "ci.test-repo").Run()
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
		{"ci.myrepo.abc123.lint", "ci.myrepo.abc123.", "lint"},
		{"ci.myrepo.abc123.tests", "ci.myrepo.abc123.", "tests"},
		{"lint", "ci.myrepo.abc123.", "lint"}, // no prefix match
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
		{Name: "ci.myrepo.abc123.lint", PID: "1"},
		{Name: "ci.myrepo.abc123.tests", PID: "2"},
		{Name: "ci.other.xyz.lint", PID: "3"},
	}

	filtered := filterSessions(sessions, "ci.myrepo.abc123.")
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

func TestExtractJobID(t *testing.T) {
	tests := []struct {
		runnerName string
		want       string
	}{
		{"ci.myrepo.abc123.runner", "abc123"},
		{"ci.test-repo.006d0847.runner", "006d0847"},
		{"ci.my_org.project.abc123.runner", "project.abc123"}, // name with underscore
	}

	for _, tt := range tests {
		got := extractJobID(tt.runnerName)
		if got != tt.want {
			t.Errorf("extractJobID(%q) = %q, want %q", tt.runnerName, got, tt.want)
		}
	}
}

func TestExtractJobPrefix(t *testing.T) {
	tests := []struct {
		sessionName string
		want        string
	}{
		{"ci.myrepo.abc123.lint", "ci.myrepo.abc123."},
		{"ci.myrepo.abc123.runner", "ci.myrepo.abc123."},
		{"ci.myrepo.abc123.tests", "ci.myrepo.abc123."},
		{"ci.name.jobID.step.substep", "ci.name.jobID."},
		{"ci.a.b", ""}, // too few parts
	}

	for _, tt := range tests {
		got := extractJobPrefix(tt.sessionName)
		if got != tt.want {
			t.Errorf("extractJobPrefix(%q) = %q, want %q", tt.sessionName, got, tt.want)
		}
	}
}

func TestFindRunningJobs(t *testing.T) {
	output := `name=ci.myrepo.abc123.runner	pid=100	clients=0	created=1777519944	start_dir=/home/erock
  name=ci.myrepo.abc123.lint	pid=101	clients=0	created=1777519944	start_dir=/home/erock
  name=ci.myrepo.abc123.tests	pid=102	clients=0	created=1777519944	start_dir=/home/erock	ended=1777519986	exit_code=0
  name=ci.myrepo.def456.runner	pid=103	clients=0	created=1777519944	start_dir=/home/erock	ended=1777519986	exit_code=0
  name=ci.other.abc123.runner	pid=104	clients=0	created=1777519944	start_dir=/home/erock
→ name=d.build.1	pid=549652	clients=0	created=1777513430	start_dir=/home/erock`

	runners, sessions := findRunningJobsFromOutput(output, "myrepo")
	if len(runners) != 1 {
		t.Fatalf("expected 1 running job, got %d: %v", len(runners), runners)
	}
	if runners[0] != "ci.myrepo.abc123.runner" {
		t.Errorf("expected runner ci.myrepo.abc123.runner, got %q", runners[0])
	}
	if len(sessions) != 6 {
		t.Errorf("expected 6 total sessions, got %d", len(sessions))
	}
}

func findRunningJobsFromOutput(output, name string) ([]string, []SessionInfo) {
	sessions := parseZMXList(output)
	var runners []string
	for _, s := range sessions {
		if strings.HasPrefix(s.Name, "ci."+name+".") && strings.HasSuffix(s.Name, ".runner") && s.Ended == "" {
			runners = append(runners, s.Name)
		}
	}
	return runners, sessions
}

func TestCancelEventMarshal(t *testing.T) {
	event := CancelEvent{
		Type:   "cancel",
		Name:   "myrepo",
		JobID:  "abc123",
		Reason: "duplicate_event",
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded CancelEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Type != "cancel" {
		t.Errorf("expected type cancel, got %q", decoded.Type)
	}
	if decoded.Name != "myrepo" {
		t.Errorf("expected name myrepo, got %q", decoded.Name)
	}
	if decoded.JobID != "abc123" {
		t.Errorf("expected job_id abc123, got %q", decoded.JobID)
	}
	if decoded.Reason != "duplicate_event" {
		t.Errorf("expected reason duplicate_event, got %q", decoded.Reason)
	}
}

func TestKillSessionsEmpty(t *testing.T) {
	// Should not error with empty list
	if err := killSessions(nil); err != nil {
		t.Errorf("killSessions(nil) = %v, want nil", err)
	}
	if err := killSessions([]string{}); err != nil {
		t.Errorf("killSessions([]) = %v, want nil", err)
	}
}

func TestResolveJobExitCode(t *testing.T) {
	tests := []struct {
		name       string
		sessions   []SessionInfo
		wantCode   int
		wantStatus string
	}{
		{
			name: "all success",
			sessions: []SessionInfo{
				{Name: "ci.repo.abc.runner", ExitCode: "0", Ended: "1"},
				{Name: "ci.repo.abc.step1", ExitCode: "0", Ended: "1"},
			},
			wantCode:   0,
			wantStatus: "success",
		},
		{
			name: "runner failed",
			sessions: []SessionInfo{
				{Name: "ci.repo.abc.runner", ExitCode: "1", Ended: "1"},
				{Name: "ci.repo.abc.step1", ExitCode: "0", Ended: "1"},
			},
			wantCode:   1,
			wantStatus: "failed",
		},
		{
			name: "child failed, runner says 0 (defensive)",
			sessions: []SessionInfo{
				{Name: "ci.repo.abc.runner", ExitCode: "0", Ended: "1"},
				{Name: "ci.repo.abc.step1", ExitCode: "0", Ended: "1"},
				{Name: "ci.repo.abc.step2", ExitCode: "2", Ended: "1"},
			},
			wantCode:   2,
			wantStatus: "failed",
		},
		{
			name: "worst child exit code wins",
			sessions: []SessionInfo{
				{Name: "ci.repo.abc.runner", ExitCode: "0", Ended: "1"},
				{Name: "ci.repo.abc.step1", ExitCode: "1", Ended: "1"},
				{Name: "ci.repo.abc.step2", ExitCode: "3", Ended: "1"},
			},
			wantCode:   3,
			wantStatus: "failed",
		},
		{
			name: "no runner session",
			sessions: []SessionInfo{
				{Name: "ci.repo.abc.step1", ExitCode: "0", Ended: "1"},
			},
			wantCode:   0,
			wantStatus: "success",
		},
		{
			name: "sessions not yet ended (no exit code)",
			sessions: []SessionInfo{
				{Name: "ci.repo.abc.runner", ExitCode: "", Ended: ""},
				{Name: "ci.repo.abc.step1", ExitCode: "", Ended: ""},
			},
			wantCode:   0,
			wantStatus: "success",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, status := resolveJobExitCode(tt.sessions)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			if status != tt.wantStatus {
				t.Errorf("status = %q, want %q", status, tt.wantStatus)
			}
		})
	}
}

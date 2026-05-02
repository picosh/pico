package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/utils/pipe"
)

type WorkspaceFactory func(cfg *Cfg, logger *slog.Logger, source string) Workspace

func defaultWorkspaceFactory(cfg *Cfg, logger *slog.Logger, source string) Workspace {
	return &WorkspaceRsync{
		Cfg:    cfg,
		Logger: logger,
		Source: source,
	}
}

type Cfg struct {
	Logger              *slog.Logger
	Ctx                 context.Context
	Cancel              context.CancelFunc
	KeyLocation         string
	CertificateLocation string
	ArtifactDir         string
	ArtifactDest        string
	Event               string        // event JSON passed via --event flag
	EventSource         io.ReadCloser // when set, used directly as the event source (for testing)
	MonitorInterval     time.Duration
	NewWorkspace        WorkspaceFactory
}

type Event struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	Workspace string `json:"workspace"`
}

type CancelEvent struct {
	Type   string `json:"type"` // "cancel"
	Name   string `json:"name"`
	JobID  string `json:"job_id"`
	Reason string `json:"reason"` // "duplicate_event", "manual", "gc"
}

func NewCfg() *Cfg {
	var keyLoc, certLoc, artifactDir, artifactDest, event string
	var monitorInterval time.Duration
	flag.StringVar(&keyLoc, "pk", "", "ssh private key used to authenticate with pico services")
	flag.StringVar(&certLoc, "ck", "", "ssh certificate public key used to authenticate with pico services (only required if using ssh certificates)")
	flag.StringVar(&artifactDir, "artifact-dir", "/tmp/pico-ci-artifacts", "local directory to stage artifacts")
	flag.StringVar(&artifactDest, "artifact-dest", "", "rsync destination for artifacts (e.g. host:/path/)")
	flag.StringVar(&event, "event", "", "event JSON to run (alternative to reading from stdin)")
	flag.DurationVar(&monitorInterval, "monitor-interval", 5*time.Second, "interval for monitoring zmx sessions")
	flag.Parse()

	logger := shared.CreateLogger("ci", false)
	ctx, cancel := context.WithCancel(context.Background())
	return &Cfg{
		NewWorkspace:        defaultWorkspaceFactory,
		Logger:              logger.With("key_loc", keyLoc, "cert_loc", certLoc),
		Ctx:                 ctx,
		Cancel:              cancel,
		KeyLocation:         keyLoc,
		CertificateLocation: certLoc,
		ArtifactDir:         artifactDir,
		ArtifactDest:        artifactDest,
		Event:               event,
		MonitorInterval:     monitorInterval,
	}
}

func main() {
	cfg := NewCfg()
	cmd := flag.Arg(0)

	cfg.Logger.Info("setting up ci", "cfg", cfg)
	cfg.Logger.Info("running cmd", "cmd", cmd)

	switch cmd {
	case "runner":
		if err := RunRunner(cfg); err != nil {
			cfg.Logger.Error("runner", "err", err)
			os.Exit(1)
		}
	case "cancel":
		if err := runCancel(cfg); err != nil {
			cfg.Logger.Error("cancel", "err", err)
			os.Exit(1)
		}
	case "gc":
		if err := runGC(cfg); err != nil {
			cfg.Logger.Error("gc", "err", err)
			os.Exit(1)
		}
	case "monitor":
		if err := runMonitor(cfg); err != nil {
			cfg.Logger.Error("monitor", "err", err)
			os.Exit(1)
		}
	case "status":
		cfg.Logger.Info("running status updater")
	case "orca":
		cfg.Logger.Info("running orchastrator")
	default:
		cfg.Logger.Error("must provide cmd")
		os.Exit(1)
	}
}

func RunRunner(cfg *Cfg) error {
	var payload string
	if cfg.EventSource != nil {
		data, err := io.ReadAll(cfg.EventSource)
		if err != nil {
			return fmt.Errorf("read event source: %w", err)
		}
		payload = strings.TrimSpace(string(data))
	} else if cfg.Event != "" {
		payload = cfg.Event
	} else {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		payload = strings.TrimSpace(string(data))
	}

	var eventData Event
	if err := json.Unmarshal([]byte(payload), &eventData); err != nil {
		return fmt.Errorf("unmarshal event: %w", err)
	}

	cfg.Logger.Info("running job", "event", eventData)

	return eventHandler(cfg, &eventData)
}

type Workspace interface {
	Setup() error
	Cleanup() error
	GetDir() string
}

type WorkspaceRsync struct {
	Cfg    *Cfg
	Logger *slog.Logger
	Source string
	Dest   string
}

func (w *WorkspaceRsync) Setup() error {
	tempDir, err := os.MkdirTemp("", "pico-ci-*")
	if err != nil {
		return err
	}
	w.Dest = tempDir

	log := w.Logger.With("source", w.Source, "dest", w.Dest)
	log.Info("cloning workspace")

	var cmd *exec.Cmd
	if w.Cfg.KeyLocation != "" {
		sshcmd := fmt.Sprintf(
			"-i %s -o IdentitiesOnly=yes -o CertificateFile %s",
			w.Cfg.KeyLocation,
			w.Cfg.CertificateLocation,
		)
		cmd = exec.Command("rsync", "-e", sshcmd, "-rv", `--exclude="/.git"`, w.Source+"/", w.Dest+"/")
	} else {
		cmd = exec.Command("rsync", "-rv", `--exclude="/.git"`, w.Source+"/", w.Dest+"/")
	}
	return runCmd(cmd, log)
}

func (w *WorkspaceRsync) Cleanup() error {
	// return os.RemoveAll(w.Dest)
	return nil
}

func (w *WorkspaceRsync) GetDir() string {
	return w.Dest
}

type JobEngine struct {
	Wk     Workspace
	Logger *slog.Logger
	Cfg    *Cfg
	Ev     *Event
	JobID  string
}

type SessionInfo struct {
	Name     string `json:"name"`
	Short    string `json:"short"`
	PID      string `json:"pid"`
	Clients  string `json:"clients"`
	Created  string `json:"created"`
	StartDir string `json:"start_dir"`
	Ended    string `json:"ended"`
	ExitCode string `json:"exit_code"`
}

type StatusPayload struct {
	Name     string        `json:"name"`
	JobID    string        `json:"job_id"`
	Status   string        `json:"status"`
	ExitCode *int          `json:"exit_code"`
	Sessions []SessionInfo `json:"sessions"`
}

func (eng *JobEngine) Setup() error {
	return eng.Wk.Setup()
}

func (eng *JobEngine) Run() error {
	manifest, err := eng.getManifest()
	if err != nil {
		return err
	}

	prefix := fmt.Sprintf("ci.%s.%s.", eng.Ev.Name, eng.JobID)

	log := eng.Logger.With("manifest", manifest, "prefix", prefix)
	log.Info("starting runner session")

	evStr := fmt.Sprintf("PICO_CI_EVENT=%s", eng.Ev.Type)
	jobStr := fmt.Sprintf("ZMX_SESSION_PREFIX=%s", prefix)

	cmd := exec.Command("zmx", "run", "runner", "-d", "bash", manifest)
	cmd.Env = append(os.Environ(), evStr, jobStr)
	cmd.Dir = eng.Wk.GetDir()

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("start runner session: %w", err)
	}

	return nil
}

func (eng *JobEngine) Cleanup() error {
	return eng.Wk.Cleanup()
}

func (eng *JobEngine) getManifest() (string, error) {
	fnames := []string{"pico.sh"}
	for _, manifest := range fnames {
		path := filepath.Join(eng.Wk.GetDir(), manifest)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("manifest not found in %s", eng.Wk.GetDir())
}

func eventHandler(cfg *Cfg, eventData *Event) error {
	log := cfg.Logger.With("event", eventData)
	log.Info("event payload")

	jobID := generateJobID(eventData.Name, eventData.Workspace)
	log = log.With("job_id", jobID)

	wk := cfg.NewWorkspace(cfg, log, eventData.Workspace)
	eng := &JobEngine{
		Logger: log,
		Cfg:    cfg,
		Wk:     wk,
		Ev:     eventData,
		JobID:  jobID,
	}
	defer func() {
		if err := eng.Cleanup(); err != nil {
			cfg.Logger.Error("engine cleanup", "err", err)
		}
	}()

	if err := eng.Setup(); err != nil {
		return fmt.Errorf("setup: %w", err)
	}

	if err := eng.Run(); err != nil {
		return fmt.Errorf("run: %w", err)
	}

	log.Info("job launched (fire-and-forget)")
	return nil
}

// runMonitor is a long-lived daemon that polls all ci.* zmx sessions,
// publishes status updates, stages artifacts, and syncs to destination.
func runMonitor(cfg *Cfg) error {
	log := cfg.Logger.With("cmd", "monitor")

	var publisher io.WriteCloser
	if cfg.KeyLocation != "" {
		publisher = createStatusPublisher(cfg, log)
	} else {
		statusPath := filepath.Join(cfg.ArtifactDir, "status.jsonl")
		f, err := os.OpenFile(statusPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("open status file %s: %w", statusPath, err)
		}
		log.Info("writing status to file (no SSH keys)", "path", statusPath)
		publisher = f
	}
	defer func() {
		if err := publisher.Close(); err != nil {
			log.Error("close status publisher", "err", err)
		}
	}()

	ticker := time.NewTicker(cfg.MonitorInterval)
	defer ticker.Stop()

	log.Info("monitor started", "interval", cfg.MonitorInterval)

	for {
		select {
		case <-cfg.Ctx.Done():
			log.Info("context cancelled, stopping monitor")
			return cfg.Ctx.Err()
		case <-ticker.C:
		}

		if err := monitorTick(cfg, log, publisher); err != nil {
			log.Error("monitor tick", "err", err)
		}
	}
}

// monitorTick performs a single monitoring pass over all ci.* sessions.
func monitorTick(cfg *Cfg, log *slog.Logger, publisher io.Writer) error {
	// a. zmx list → filter ci.* sessions
	listOutput, err := exec.Command("zmx", "list").CombinedOutput()
	if err != nil {
		return fmt.Errorf("zmx list: %w", err)
	}
	sessions := parseZMXList(string(listOutput))
	ciSessions := filterCISessions(sessions)

	if len(ciSessions) == 0 {
		return nil
	}

	// b. Group by job prefix: ci.<name>.<jobID>.
	groups := groupSessionsByJob(ciSessions)

	// c. Process each job group
	for prefix, group := range groups {
		name, jobID := parseJobPrefix(prefix)
		if name == "" {
			continue
		}

		if allCompleted(group) {
			// Stage artifacts for each session
			for _, s := range group {
				html, err := fetchHistoryHTML(s.Name)
				if err != nil {
					log.Error("fetch history", "session", s.Name, "err", err)
					continue
				}
				if err := stageArtifact(cfg.ArtifactDir, name, jobID, s.Short, html); err != nil {
					log.Error("stage artifact", "session", s.Short, "err", err)
				}
			}

			// Publish final status
			exitCode, status := resolveJobExitCode(group)
			payload := StatusPayload{
				Name:     name,
				JobID:    jobID,
				Status:   status,
				ExitCode: &exitCode,
				Sessions: group,
			}
			if err := publishStatus(publisher, payload); err != nil {
				log.Error("publish final status", "name", name, "job_id", jobID, "err", err)
			}
		} else {
			// Publish running status
			payload := StatusPayload{
				Name:     name,
				JobID:    jobID,
				Status:   "running",
				ExitCode: nil,
				Sessions: group,
			}
			if err := publishStatus(publisher, payload); err != nil {
				log.Error("publish status", "name", name, "job_id", jobID, "err", err)
			}
		}
	}

	// d. Sync artifacts once per tick
	if err := syncArtifacts(cfg, log); err != nil {
		log.Error("sync artifacts", "err", err)
	}

	return nil
}

// filterCISessions returns only sessions with ci. prefix.
func filterCISessions(sessions []SessionInfo) []SessionInfo {
	var filtered []SessionInfo
	for _, s := range sessions {
		if strings.HasPrefix(s.Name, "ci.") {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

// groupSessionsByJob groups sessions by their job prefix (ci.<name>.<jobID>.).
func groupSessionsByJob(sessions []SessionInfo) map[string][]SessionInfo {
	groups := make(map[string][]SessionInfo)
	for _, s := range sessions {
		prefix := extractJobPrefix(s.Name)
		if prefix == "" {
			continue
		}
		// Set the Short name
		s.Short = strings.TrimPrefix(s.Name, prefix)
		groups[prefix] = append(groups[prefix], s)
	}
	return groups
}

// parseJobPrefix extracts name and jobID from a prefix like "ci.<name>.<jobID>.".
func parseJobPrefix(prefix string) (name, jobID string) {
	// ci.name.jobID. -> ["ci", "name", "jobID", ""]
	parts := strings.Split(prefix, ".")
	if len(parts) < 4 {
		return "", ""
	}
	return parts[1], parts[2]
}

// resolveJobExitCode determines the job's exit code from its sessions.
// Defensive: if any child session failed, the job failed regardless of the
// runner's exit code. This protects against bad pico.sh scripts that exit 0
// without waiting for children.
func resolveJobExitCode(sessions []SessionInfo) (int, string) {
	var runnerExit *int
	var worstChild *int // highest non-zero child exit code

	for _, s := range sessions {
		if s.ExitCode == "" {
			continue
		}
		var code int
		if _, err := fmt.Sscanf(s.ExitCode, "%d", &code); err != nil {
			continue
		}

		if strings.HasSuffix(s.Name, ".runner") {
			runnerExit = &code
		} else if code != 0 {
			if worstChild == nil || code > *worstChild {
				worstChild = &code
			}
		}
	}

	// Any child failure overrides the runner — defensive against bad scripts
	if worstChild != nil {
		return *worstChild, "failed"
	}
	if runnerExit != nil && *runnerExit != 0 {
		return *runnerExit, "failed"
	}
	return 0, "success"
}

func generateJobID(name, workspace string) string {
	return jobIDFor(name, workspace, time.Now().UnixNano())
}

func jobIDFor(name, workspace string, ts int64) string {
	h := sha256.Sum256([]byte(name + workspace + fmt.Sprintf("%d", ts)))
	return fmt.Sprintf("%x", h[:4])
}

func shortSessionName(session, prefix string) string {
	return strings.TrimPrefix(session, prefix)
}

func parseZMXList(output string) []SessionInfo {
	var sessions []SessionInfo
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Strip leading arrow/space prefix
		line = strings.TrimPrefix(line, "→ ")
		line = strings.TrimSpace(line)

		fields := strings.FieldsFunc(line, func(r rune) bool {
			return r == '\t'
		})

		var si SessionInfo
		for _, field := range fields {
			parts := strings.SplitN(field, "=", 2)
			if len(parts) != 2 {
				continue
			}
			switch parts[0] {
			case "name":
				si.Name = parts[1]
			case "pid":
				si.PID = parts[1]
			case "clients":
				si.Clients = parts[1]
			case "created":
				si.Created = parts[1]
			case "start_dir":
				si.StartDir = parts[1]
			case "ended":
				si.Ended = parts[1]
			case "exit_code":
				si.ExitCode = parts[1]
			}
		}
		if si.Name != "" {
			sessions = append(sessions, si)
		}
	}
	return sessions
}

func fetchHistoryHTML(sessionName string) (string, error) {
	cmd := exec.Command("zmx", "history", sessionName, "--html")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func publishStatus(w io.Writer, payload StatusPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

func createStatusPublisher(cfg *Cfg, logger *slog.Logger) io.WriteCloser {
	logger.Info("creating pipe publisher", "topic", "build.status")
	info := shared.NewPicoPipeClient()
	info.KeyLocation = cfg.KeyLocation
	info.CertificateLocation = cfg.CertificateLocation
	pub := pipe.NewReconnectReadWriteCloser(
		cfg.Ctx,
		logger,
		info,
		"pub build.status -b=false",
		"pub build.status -b=false",
		100,
		-1,
	)
	return pub
}

func stageArtifact(dir, name, jobID, short, html string) error {
	path := filepath.Join(dir, name, jobID, short+".html")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(html), 0644)
}

func syncArtifacts(cfg *Cfg, log *slog.Logger) error {
	if cfg.ArtifactDest == "" {
		return nil
	}
	sshArgs := fmt.Sprintf(
		"-i %s -o IdentitiesOnly=yes -o CertificateFile %s",
		cfg.KeyLocation,
		cfg.CertificateLocation,
	)
	cmd := exec.Command("rsync", "-e", sshArgs, "-rv", cfg.ArtifactDir+"/", cfg.ArtifactDest)
	return runCmd(cmd, log)
}

func filterSessions(sessions []SessionInfo, prefix string) []SessionInfo {
	var filtered []SessionInfo
	for _, s := range sessions {
		if strings.HasPrefix(s.Name, prefix) {
			cs := s
			cs.Short = shortSessionName(s.Name, prefix)
			filtered = append(filtered, cs)
		}
	}
	return filtered
}

func allCompleted(sessions []SessionInfo) bool {
	for _, s := range sessions {
		if s.Ended == "" {
			return false
		}
	}
	return true
}

func runCmd(cmd *exec.Cmd, log *slog.Logger) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			log.Info("cmd stdout", "text", scanner.Text())
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Error("cmd stderr", "text", scanner.Text())
		}
	}()

	return cmd.Wait()
}

// runCancel reads an event from stdin and cancels any running job with matching name+type.
func runCancel(cfg *Cfg) error {
	log := cfg.Logger.With("cmd", "cancel")

	// Read event from stdin
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return fmt.Errorf("no input on stdin")
	}

	var event Event
	if err := json.Unmarshal([]byte(scanner.Text()), &event); err != nil {
		return fmt.Errorf("unmarshal event: %w", err)
	}

	log = log.With("name", event.Name, "type", event.Type)
	log.Info("cancelling jobs")

	// Find running jobs matching name+type
	runnerSessions, sessions := findRunningJobs(event.Name)
	if len(runnerSessions) == 0 {
		log.Info("no running jobs to cancel")
		return nil
	}

	// Create publishers
	statusPub := createStatusPublisher(cfg, log)
	defer func() {
		if err := statusPub.Close(); err != nil {
			log.Error("close status publisher", "err", err)
		}
	}()
	cancelPub := createCancelPublisher(cfg, log)
	defer func() {
		if err := cancelPub.Close(); err != nil {
			log.Error("close cancel publisher", "err", err)
		}
	}()

	for _, runnerName := range runnerSessions {
		// Extract jobID from runner session name: ci.<name>.<jobID>.runner
		jobID := extractJobID(runnerName)
		log := log.With("job_id", jobID)

		// Kill the runner session (cascades to children)
		if err := killSessions([]string{runnerName}); err != nil {
			log.Error("kill runner session", "err", err)
			continue
		}
		log.Info("cancelled runner session")

		// Publish cancelled status
		matched := filterSessions(sessions, fmt.Sprintf("ci.%s.%s.", event.Name, jobID))
		statusPayload := StatusPayload{
			Name:     event.Name,
			JobID:    jobID,
			Status:   "cancelled",
			ExitCode: nil,
			Sessions: matched,
		}
		if err := publishStatus(statusPub, statusPayload); err != nil {
			log.Error("publish cancelled status", "err", err)
		}

		// Publish cancel event
		cancelEvent := CancelEvent{
			Type:   "cancel",
			Name:   event.Name,
			JobID:  jobID,
			Reason: "duplicate_event",
		}
		if err := publishCancelEvent(cancelPub, cancelEvent); err != nil {
			log.Error("publish cancel event", "err", err)
		}
	}

	return nil
}

// findRunningJobs finds all active runner sessions for a given name.
// Returns runner session names and all sessions for reference.
func findRunningJobs(name string) ([]string, []SessionInfo) {
	listOutput, err := exec.Command("zmx", "list").CombinedOutput()
	if err != nil {
		return nil, nil
	}

	sessions := parseZMXList(string(listOutput))
	var runners []string
	for _, s := range sessions {
		// Match ci.<name>.*.runner sessions that are still active (no ended)
		if strings.HasPrefix(s.Name, "ci."+name+".") && strings.HasSuffix(s.Name, ".runner") && s.Ended == "" {
			runners = append(runners, s.Name)
		}
	}
	return runners, sessions
}

// extractJobID extracts the jobID from a runner session name like ci.<name>.<jobID>.runner.
func extractJobID(runnerName string) string {
	// ci.<name>.<jobID>.runner
	// Remove "ci." prefix and ".runner" suffix, then take the part after the first "."
	name := strings.TrimSuffix(runnerName, ".runner")
	name = strings.TrimPrefix(name, "ci.")
	// name is now <name>.<jobID>, take the jobID part
	parts := strings.SplitN(name, ".", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}

// killSessions kills zmx sessions by name.
func killSessions(names []string) error {
	if len(names) == 0 {
		return nil
	}
	args := append([]string{"kill"}, names...)
	cmd := exec.Command("zmx", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("zmx kill: %s: %w", string(output), err)
	}
	return nil
}

// publishCancelEvent publishes a CancelEvent to the build.cancel pipe topic.
func publishCancelEvent(w io.Writer, event CancelEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// createCancelPublisher creates a pipe publisher for the build.cancel topic.
func createCancelPublisher(cfg *Cfg, logger *slog.Logger) io.WriteCloser {
	logger.Info("creating pipe publisher", "topic", "build.cancel")
	info := shared.NewPicoPipeClient()
	info.KeyLocation = cfg.KeyLocation
	info.CertificateLocation = cfg.CertificateLocation
	pub := pipe.NewReconnectReadWriteCloser(
		cfg.Ctx,
		logger,
		info,
		"pub build.cancel -b=false",
		"pub build.cancel -b=false",
		100,
		-1,
	)
	return pub
}

// runGC deletes completed CI zmx sessions that are not part of a running job.
func runGC(cfg *Cfg) error {
	log := cfg.Logger.With("cmd", "gc")
	log.Info("running garbage collection")

	listOutput, err := exec.Command("zmx", "list").CombinedOutput()
	if err != nil {
		return fmt.Errorf("zmx list: %w", err)
	}

	sessions := parseZMXList(string(listOutput))

	// Group ci. sessions by job prefix: ci.<name>.<jobID>.
	groups := make(map[string][]SessionInfo)
	for _, s := range sessions {
		if !strings.HasPrefix(s.Name, "ci.") {
			continue
		}
		// Extract job prefix: ci.<name>.<jobID>.
		prefix := extractJobPrefix(s.Name)
		if prefix == "" {
			continue
		}
		groups[prefix] = append(groups[prefix], s)
	}

	// For each group, if all sessions are completed, kill them.
	var toKill []string
	for prefix, group := range groups {
		allDone := true
		for _, s := range group {
			if s.Ended == "" {
				allDone = false
				break
			}
		}
		if allDone {
			log.Info("completed job, scheduling for gc", "prefix", prefix, "sessions", len(group))
			toKill = append(toKill, group[0].Name)
		}
	}

	if len(toKill) == 0 {
		log.Info("no sessions to garbage collect")
		return nil
	}

	if err := killSessions(toKill); err != nil {
		return fmt.Errorf("kill sessions: %w", err)
	}

	log.Info("garbage collection complete", "killed", len(toKill))
	return nil
}

// extractJobPrefix extracts the job prefix from a session name.
// ci.<name>.<jobID>.<step> -> ci.<name>.<jobID>.
func extractJobPrefix(sessionName string) string {
	// Find the third dot (after ci.<name>.<jobID>)
	// ci.name.jobID.step -> ci.name.jobID.
	parts := strings.Split(sessionName, ".")
	if len(parts) < 4 {
		return ""
	}
	return parts[0] + "." + parts[1] + "." + parts[2] + "."
}

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
	"golang.org/x/term"
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
	StdinEvents         bool          // true when stdin is not a terminal
	EventSource         io.ReadCloser // when set, used directly as the event source (for testing)
	StatusFile          string
	MonitorInterval     time.Duration
	NewWorkspace        WorkspaceFactory
}

type Event struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	Workspace string `json:"workspace"`
}

func NewCfg() *Cfg {
	var keyLoc, certLoc, artifactDir, artifactDest, statusFile string
	var monitorInterval time.Duration
	flag.StringVar(&keyLoc, "pk", "", "ssh private key used to authenticate with pico services")
	flag.StringVar(&certLoc, "ck", "", "ssh certificate public key used to authenticate with pico services (only required if using ssh certificates)")
	flag.StringVar(&artifactDir, "artifact-dir", "/tmp/pico-ci-artifacts", "local directory to stage artifacts")
	flag.StringVar(&artifactDest, "artifact-dest", "", "rsync destination for artifacts (e.g. host:/path/)")
	flag.StringVar(&statusFile, "status-file", "", "file to write status to instead of pipe (one JSON status per line)")
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
		StdinEvents:         !term.IsTerminal(int(os.Stdin.Fd())),
		StatusFile:          statusFile,
		MonitorInterval:     monitorInterval,
	}
}

func main() {
	cmd := flag.Arg(0)
	cfg := NewCfg()

	cfg.Logger.Info("setting up ci", "cfg", cfg)
	cfg.Logger.Info("running cmd", "cmd", cmd)

	switch cmd {
	case "runner":
		if err := RunRunner(cfg); err != nil {
			cfg.Logger.Error("runner", "err", err)
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
	var eventSource io.ReadCloser
	if cfg.EventSource != nil {
		eventSource = cfg.EventSource
	} else if cfg.StdinEvents {
		eventSource = os.Stdin
	} else {
		eventSource = createEventSubscriber(cfg, cfg.Logger)
	}

	var statusSink io.WriteCloser
	if cfg.StatusFile != "" {
		f, err := os.OpenFile(cfg.StatusFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("open status file: %w", err)
		}
		statusSink = f
	} else {
		statusSink = createStatusPublisher(cfg, cfg.Logger)
	}
	defer func() {
		if err := statusSink.Close(); err != nil {
			cfg.Logger.Error("close status sink", "err", err)
		}
	}()

	cfg.Logger.Info("waiting for events")
	scanner := bufio.NewScanner(eventSource)
	scanner.Buffer(make([]byte, 32*1024), 32*1024)
	for scanner.Scan() {
		select {
		case <-cfg.Ctx.Done():
			cfg.Logger.Info("context cancelled, stopping event loop")
			return cfg.Ctx.Err()
		default:
		}

		payload := strings.TrimSpace(scanner.Text())
		var eventData Event
		if err := json.Unmarshal([]byte(payload), &eventData); err != nil {
			cfg.Logger.Error("json unmarshal", "err", err)
			continue
		}
		eventHandler(cfg, statusSink, &eventData)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanning events: %w", err)
	}
	return nil
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
	Cmd    *exec.Cmd
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

	log := eng.Logger.With("manifest", manifest)
	prefix := fmt.Sprintf("%s.%s.", eng.Ev.Name, eng.JobID)
	evStr := fmt.Sprintf("PICO_CI_EVENT=%s", eng.Ev.Type)
	jobStr := fmt.Sprintf("ZMX_SESSION_PREFIX=%s", prefix)
	cmd := exec.Command("bash", manifest)
	cmd.Env = append(os.Environ(), evStr)
	cmd.Env = append(os.Environ(), jobStr)
	cmd.Dir = eng.Wk.GetDir()

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
	eng.Cmd = cmd

	// Stream stdout/stderr with context-aware shutdown
	go eng.streamOutput(stdout, log, "stdout")
	go eng.streamOutput(stderr, log, "stderr")

	return nil
}

func (eng *JobEngine) streamOutput(reader io.Reader, log *slog.Logger, label string) {
	done := eng.Cfg.Ctx.Done()
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		select {
		case <-done:
			return
		default:
		}
		if label == "stderr" {
			log.Error("pico.sh stderr", "text", scanner.Text())
		} else {
			log.Info("pico.sh stdout", "text", scanner.Text())
		}
	}
}

func (eng *JobEngine) Cleanup() error {
	return eng.Wk.Cleanup()
}

func (eng *JobEngine) Monitor(publisher io.WriteCloser) error {
	prefix := fmt.Sprintf("%s.%s.", eng.Ev.Name, eng.JobID)
	ticker := time.NewTicker(eng.Cfg.MonitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-eng.Cfg.Ctx.Done():
			eng.Logger.Info("context cancelled, stopping monitor")
			return eng.Cfg.Ctx.Err()
		case <-ticker.C:
		}

		// a. zmx list → filter sessions
		listOutput, err := exec.Command("zmx", "list").CombinedOutput()
		if err != nil {
			eng.Logger.Error("zmx list", "err", err)
			continue
		}
		sessions := parseZMXList(string(listOutput))
		matched := filterSessions(sessions, prefix)

		// b. For each matched session, fetch HTML and stage artifact
		for _, s := range matched {
			html, err := fetchHistoryHTML(s.Name)
			if err != nil {
				eng.Logger.Error("fetch history", "session", s.Name, "err", err)
				continue
			}
			err = stageArtifact(eng.Cfg.ArtifactDir, eng.Ev.Name, eng.JobID, s.Short, html)
			if err != nil {
				eng.Logger.Error("stage artifact", "session", s.Short, "err", err)
			}
		}

		// c. Sync artifacts to destination
		if err := syncArtifacts(eng.Cfg, eng.Logger); err != nil {
			eng.Logger.Error("sync artifacts", "err", err)
		}

		// d. Publish status
		payload := StatusPayload{
			Name:     eng.Ev.Name,
			JobID:    eng.JobID,
			Status:   "running",
			ExitCode: nil,
			Sessions: matched,
		}
		if err := publishStatus(publisher, payload); err != nil {
			eng.Logger.Error("publish status", "err", err)
		}

		// e. Check if all sessions have ended
		if allCompleted(matched) {
			exitCode, status := eng.waitForCommand()
			finalPayload := StatusPayload{
				Name:     eng.Ev.Name,
				JobID:    eng.JobID,
				Status:   status,
				ExitCode: &exitCode,
				Sessions: matched,
			}
			if err := publishStatus(publisher, finalPayload); err != nil {
				eng.Logger.Error("publish final status", "err", err)
			}
			return nil
		}
	}
}

func (eng *JobEngine) waitForCommand() (int, string) {
	err := eng.Cmd.Wait()
	if err == nil {
		return 0, "success"
	}
	if exitError, ok := err.(*exec.ExitError); ok {
		return int(exitError.ExitCode()), "failed"
	}
	return 1, "failed"
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

func eventHandler(cfg *Cfg, publisher io.WriteCloser, eventData *Event) {
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
		err := eng.Cleanup()
		if err != nil {
			cfg.Logger.Error("engine cleanup", "err", err)
		}
	}()

	err := eng.Setup()
	if err != nil {
		log.Error("setup", "err", err)
		return
	}

	err = eng.Run()
	if err != nil {
		log.Error("run failure", "err", err)
		return
	}

	err = eng.Monitor(publisher)
	if err != nil {
		log.Error("monitor", "err", err)
		return
	}
	log.Info("job complete")
}

func createEventSubscriber(cfg *Cfg, logger *slog.Logger) io.ReadCloser {
	logger.Info("subscribing to pipe", "topic", "build.jobs")
	info := shared.NewPicoPipeClient()
	info.KeyLocation = cfg.KeyLocation
	info.CertificateLocation = cfg.CertificateLocation
	send := pipe.NewReconnectReadWriteCloser(
		cfg.Ctx,
		logger,
		info,
		"sub to build.jobs",
		"sub build.jobs -k",
		100,
		-1,
	)
	return send
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

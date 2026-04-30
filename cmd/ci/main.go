package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/utils/pipe"
)

type Cfg struct {
	Logger              *slog.Logger
	Ctx                 context.Context
	KeyLocation         string
	CertificateLocation string
}

type Event struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	Workspace string `json:"workspace"`
}

func main() {
	var keyLoc string
	flag.StringVar(&keyLoc, "pk", "", "ssh private key used to authenticate with pico services (requires access to: pipe, pgs)")
	var certLoc string
	flag.StringVar(&certLoc, "ck", "", "ssh certificate public key used to authenticate with pico services (only required if using ssh certificates)")
	flag.Parse()
	cmd := flag.Arg(0)

	logger := shared.CreateLogger("ci", false)
	log := logger.With("key_loc", keyLoc, "cert_loc", certLoc)
	ctx := context.Background()
	cfg := &Cfg{
		Logger:              log,
		Ctx:                 ctx,
		KeyLocation:         keyLoc,
		CertificateLocation: certLoc,
	}
	cfg.Logger.Info("setting up ci", "cfg", cfg)
	cfg.Logger.Info("running cmd", "cmd", cmd)

	switch cmd {
	case "runner":
		psub := createSubJobs(cfg, cfg.Logger)
		cfg.Logger.Info("waiting for pipe event")
		for {
			scanner := bufio.NewScanner(psub)
			scanner.Buffer(make([]byte, 32*1024), 32*1024)
			for scanner.Scan() {
				payload := strings.TrimSpace(scanner.Text())
				var eventData Event
				err := json.Unmarshal([]byte(payload), &eventData)
				if err != nil {
					cfg.Logger.Error("json unmarshal", "err", err)
				}
				eventHandler(cfg, &eventData)
			}
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

	sshcmd := fmt.Sprintf(
		"-i %s -o IdentitiesOnly=yes -o CertificateFile %s",
		w.Cfg.KeyLocation,
		w.Cfg.CertificateLocation,
	)
	log := w.Logger.With("source", w.Source, "dest", w.Dest)
	log.Info("cloning workspace")
	cmd := exec.Command("rsync", "-e", sshcmd, "-rv", `--exclude="/.git"`, w.Source, w.Dest+"/")
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
	err := eng.Wk.Setup()
	if err != nil {
		return err
	}
	return nil
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

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			log.Info("pico.sh stdout", "text", scanner.Text())
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Error("pico.sh stderr", "text", scanner.Text())
		}
	}()

	return nil
}

func (eng *JobEngine) Cleanup() error {
	return eng.Wk.Cleanup()
}

func (eng *JobEngine) Monitor(publisher *pipe.ReconnectReadWriteCloser) error {
	prefix := fmt.Sprintf("%s.%s.", eng.Ev.Name, eng.JobID)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// a. zmx list → filter sessions
		listOutput, err := exec.Command("zmx", "list").CombinedOutput()
		if err != nil {
			eng.Logger.Error("zmx list", "err", err)
			continue
		}
		sessions := parseZMXList(string(listOutput))
		matched := filterSessions(sessions, prefix)

		// b. For each matched session, fetch HTML and send to PGS
		for _, s := range matched {
			html, err := fetchHistoryHTML(s.Name)
			if err != nil {
				eng.Logger.Error("fetch history", "session", s.Name, "err", err)
				continue
			}
			err = sendToPGS(eng.Cfg, eng.Ev.Name, eng.JobID, s.Short, html)
			if err != nil {
				eng.Logger.Error("send to pgs", "session", s.Short, "err", err)
			}
		}

		// c. Publish status
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

		// d. Check if all sessions have ended
		if allCompleted(matched) {
			err := eng.Cmd.Wait()
			exitCode := 0
			status := "success"
			if err != nil {
				if exitError, ok := err.(*exec.ExitError); ok {
					exitCode = int(exitError.ExitCode())
					status = "failed"
				} else {
					status = "failed"
				}
			}

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
			break
		}
	}

	return nil
}

func (eng *JobEngine) getManifest() (string, error) {
	fnames := []string{"pico.sh"}
	for _, manifest := range fnames {
		_, err := os.Stat(manifest)
		if err != nil {
			continue
		}
		return manifest, nil
	}
	return "", fmt.Errorf("manifest not found")
}

func eventHandler(cfg *Cfg, eventData *Event) {
	log := cfg.Logger.With("event", eventData)
	log.Info("event payload")

	jobID := generateJobID(eventData.Name, eventData.Workspace)
	log = log.With("job_id", jobID)

	wk := &WorkspaceRsync{
		Logger: log,
		Cfg:    cfg,
		Source: eventData.Workspace,
	}
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

	publisher := createStatusPublisher(cfg, log)
	defer func() {
		if err := publisher.Close(); err != nil {
			log.Error("publisher close", "err", err)
		}
	}()

	err = eng.Monitor(publisher)
	if err != nil {
		log.Error("monitor", "err", err)
		return
	}
	log.Info("job complete")
}

func createSubJobs(cfg *Cfg, logger *slog.Logger) *pipe.ReconnectReadWriteCloser {
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

func publishStatus(publisher *pipe.ReconnectReadWriteCloser, payload StatusPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = publisher.Write(append(data, '\n'))
	return err
}

func createStatusPublisher(cfg *Cfg, logger *slog.Logger) *pipe.ReconnectReadWriteCloser {
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

func sendToPGS(cfg *Cfg, name, jobID, short, html string) error {
	sshArgs := fmt.Sprintf(
		"-i %s -o IdentitiesOnly=yes -o CertificateFile %s",
		cfg.KeyLocation,
		cfg.CertificateLocation,
	)
	path := fmt.Sprintf("/private-ci/%s/%s/%s.html", name, jobID, short)
	cmd := exec.Command("ssh", sshArgs, "pgs", fmt.Sprintf("cat > %s", path))
	cmd.Stdin = strings.NewReader(html)
	return cmd.Run()
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

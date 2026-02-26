package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

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
				go eventHandler(cfg, &eventData)
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

	jobName := filepath.Base(eng.Wk.GetDir())
	log := eng.Logger.With("manifest", manifest)
	evStr := fmt.Sprintf("PICO_CI_EVENT=%s", eng.Ev.Type)
	// create a marker so we can scan for it in stdout to know when the job is complete
	// and capture the exit status
	marker := fmt.Sprintf("__ZMX_CMD_EXIT:%s:", jobName)
	// run the manifest shell script and then output the end marker
	runner := fmt.Sprintf(`bash -c './%s ; echo %s$?'`, manifest, marker)
	// run the job inside of zmx so we get session persistence
	cmd := exec.Command("zmx", "run", jobName, runner)
	cmd.Env = append(os.Environ(), evStr)
	cmd.Dir = eng.Wk.GetDir()
	err = runCmd(cmd, log)
	if err != nil {
		return err
	}

	outCmd := exec.Command("zmx", "tail", jobName)
	stdout, err := outCmd.StdoutPipe()
	if err != nil {
		return err
	}

	stderr, err := outCmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := outCmd.Start(); err != nil {
		return err
	}

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, marker) {
				status := strings.Replace(line, marker, "", 1)
				exitStatus, err := strconv.Atoi(status)
				if err != nil {
					continue
				}
				// TODO report status
				if exitStatus == 0 {
					log.Info("cmd success")
				} else {
					log.Error("cmd error", "status", exitStatus)
				}
				err = outCmd.Process.Kill()
				if err != nil {
					log.Error("cmd proc kill", "err", err)
				}
			} else {
				log.Info("cmd stdout", "text", line)
			}
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Error("cmd stderr", "text", scanner.Text())
		}
	}()

	return outCmd.Wait()
}

func (eng *JobEngine) Cleanup() error {
	return eng.Wk.Cleanup()
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
	}
	defer func() {
		err := eng.Cleanup()
		if err != nil {
			cfg.Logger.Error("engine cleanup", "err", err)
		}
	}()

	err := eng.Setup()
	if err != nil {
		log.Error("run", "err", err)
		return
	}

	err = eng.Run()
	if err != nil {
		log.Error("run failure", "err", err)
		return
	}
	log.Info("run done")
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

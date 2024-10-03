package shared

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	slogmulti "github.com/samber/slog-multi"
	"golang.org/x/crypto/ssh"
)

type SendLogWriter struct {
	SSHClient *ssh.Client
	Session   *ssh.Session
	StdinPipe io.Writer
	Done      chan struct{}
	Messages  chan []byte
	Timeout   time.Duration
	closeOnce sync.Once
	startOnce sync.Once
}

func (c *SendLogWriter) Write(data []byte) (int, error) {
	var (
		n   int
		err error
	)

	select {
	case c.Messages <- data:
		n = len(data)
	case <-time.After(c.Timeout):
		err = fmt.Errorf("unable to send data within timeout")
	}

	return n, err
}

func (c *SendLogWriter) Open() {
	go func() {
		for {
			select {
			case data := <-c.Messages:
				_, err := c.StdinPipe.Write(data)
				if err != nil {
					slog.Info("received error on write", "error", err)
				}
			case <-c.Done:
				return
			}
		}
	}()
}

func createSSHClient(remoteHost string, keyLocation string, keyPassphrase string, remoteHostname string, remoteUser string) *ssh.Client {
	if !strings.Contains(remoteHost, ":") {
		remoteHost += ":22"
	}

	rawConn, err := net.Dial("tcp", remoteHost)
	if err != nil {
		slog.Error(
			"Unable to create ssh client, tcp connection not established",
			slog.Any("error", err),
		)
		panic(err)
	}

	keyPath, err := filepath.Abs(keyLocation)
	if err != nil {
		slog.Error(
			"Unable to create ssh client, cannot find key file",
			slog.Any("error", err),
		)
		panic(err)
	}

	f, err := os.Open(keyPath)
	if err != nil {
		slog.Error(
			"Unable to create ssh client, unable to open key",
			slog.Any("error", err),
		)
		panic(err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		slog.Error(
			"Unable to create ssh client, unable to read key",
			slog.Any("error", err),
		)
		panic(err)
	}

	var signer ssh.Signer

	if keyPassphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(keyPassphrase))
	} else {
		signer, err = ssh.ParsePrivateKey(data)
	}

	if err != nil {
		slog.Error(
			"Unable to create ssh client, unable to parse key",
			slog.Any("error", err),
		)
		panic(err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(rawConn, remoteHostname, &ssh.ClientConfig{
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		User:            remoteUser,
	})
	if err != nil {
		slog.Error(
			"Unable to create ssh client, unable to create client conn",
			slog.Any("error", err),
		)
		panic(err)
	}

	sshClient := ssh.NewClient(sshConn, chans, reqs)

	return sshClient
}

func SendLogRegister(logger *slog.Logger, buffer int) (*slog.Logger, error) {
	if buffer < 0 {
		buffer = 0
	}

	currentHandler := logger.Handler()

	sshClient := createSSHClient(
		"send.pico.sh:22",
		os.Getenv("SSH_KEY"),
		os.Getenv("SSH_PASSPHRASE"),
		"send.pico.sh",
		os.Getenv("SSH_USER"),
	)

	sesh, err := sshClient.NewSession()
	if err != nil {
		return logger, nil
	}

	stdinPipe, err := sesh.StdinPipe()
	if err != nil {
		return logger, nil
	}

	err = sesh.Start("pub log-sink -b=false")
	if err != nil {
		return logger, nil
	}

	logWriter := &SendLogWriter{
		SSHClient: sshClient,
		Session:   sesh,
		StdinPipe: stdinPipe,
		Done:      make(chan struct{}),
		Messages:  make(chan []byte, buffer),
		Timeout:   10 * time.Millisecond,
	}

	logWriter.Open()

	return slog.New(
		slogmulti.Fanout(
			currentHandler,
			slog.NewJSONHandler(logWriter, &slog.HandlerOptions{
				AddSource: true,
				Level:     slog.LevelDebug,
			}),
		),
	), nil
}

var _ io.Writer = (*SendLogWriter)(nil)

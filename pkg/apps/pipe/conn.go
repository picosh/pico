package pipe

import (
	"io"
	"sync"

	"github.com/picosh/utils/pipe"
	"golang.org/x/crypto/ssh"
)

// pipeConn is pipe-web's ssh connection to pipe. Each request runs its
// command in a session of its own, which is never reopened, so the command
// ends when the request does.
type pipeConn struct {
	info *pipe.SSHClientInfo

	mu     sync.Mutex
	client *ssh.Client
}

func newPipeConn(info *pipe.SSHClientInfo) *pipeConn {
	return &pipeConn{info: info}
}

// connect returns the open connection, dialing a new one when there is none
// or when the open one is dead.
func (c *pipeConn) connect(dead *ssh.Client) (*ssh.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client != nil && c.client != dead {
		return c.client, nil
	}
	if c.client != nil {
		_ = c.client.Close()
		c.client = nil
	}
	client, err := pipe.NewSSHClient(c.info)
	if err != nil {
		return nil, err
	}
	c.client = client
	return client, nil
}

// run starts cmd on pipe. A connection that can't open a session is dialed
// again once.
func (c *pipeConn) run(cmd string) (*cmdSession, error) {
	client, err := c.connect(nil)
	if err != nil {
		return nil, err
	}
	s, err := startCmd(client, cmd)
	if err == nil {
		return s, nil
	}
	client, err = c.connect(client)
	if err != nil {
		return nil, err
	}
	return startCmd(client, cmd)
}

// cmdSession is a command running on pipe, read from its stdout and written
// to its stdin.
type cmdSession struct {
	*ssh.Session
	stdin  io.WriteCloser
	stdout io.Reader
}

func startCmd(client *ssh.Client, cmd string) (*cmdSession, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	s := &cmdSession{Session: session}
	if s.stdin, err = session.StdinPipe(); err == nil {
		if s.stdout, err = session.StdoutPipe(); err == nil {
			err = session.Start(cmd)
		}
	}
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	return s, nil
}

func (s *cmdSession) Read(p []byte) (int, error) {
	return s.stdout.Read(p)
}

func (s *cmdSession) Write(p []byte) (int, error) {
	return s.stdin.Write(p)
}

// CloseWrite sends the command EOF on its stdin.
func (s *cmdSession) CloseWrite() error {
	return s.stdin.Close()
}

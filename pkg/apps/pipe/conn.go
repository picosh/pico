package pipe

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/picosh/utils/pipe"
	"golang.org/x/crypto/ssh"
)

// dialTimeout bounds connecting to pipe, handshake included, and starting a
// command on it, so a server that stops answering can't hold up requests.
const dialTimeout = 10 * time.Second

// pipeConn is pipe-web's ssh connection to pipe. Each request runs its
// command in a session of its own, which is never reopened, so the command
// ends when the request does.
type pipeConn struct {
	info    *pipe.SSHClientInfo
	timeout time.Duration

	mu      sync.Mutex
	client  *ssh.Client
	dialing *dial
}

// dial is a connection being opened, shared by the requests waiting on it.
type dial struct {
	done   chan struct{}
	client *ssh.Client
	err    error
}

func newPipeConn(info *pipe.SSHClientInfo) *pipeConn {
	return &pipeConn{info: info, timeout: dialTimeout}
}

// connect returns the open connection, dialing a new one when there is none
// or when the open one is dead. A request that ends stops waiting, and the
// dial carries on for the requests after it.
func (c *pipeConn) connect(ctx context.Context, dead *ssh.Client) (*ssh.Client, error) {
	c.mu.Lock()
	if c.client != nil && c.client != dead {
		client := c.client
		c.mu.Unlock()
		return client, nil
	}
	if c.client != nil {
		_ = c.client.Close()
		c.client = nil
	}
	d := c.dialing
	if d == nil {
		d = &dial{done: make(chan struct{})}
		c.dialing = d
		go c.dial(d)
	}
	c.mu.Unlock()

	select {
	case <-d.done:
		return d.client, d.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *pipeConn) dial(d *dial) {
	d.client, d.err = dialSSH(c.info, c.timeout)
	c.mu.Lock()
	c.dialing = nil
	if d.err == nil {
		c.client = d.client
	}
	c.mu.Unlock()
	close(d.done)
}

// run starts cmd on pipe. A connection that can't open a session is dialed
// again once.
func (c *pipeConn) run(ctx context.Context, cmd string) (*cmdSession, error) {
	client, err := c.connect(ctx, nil)
	if err != nil {
		return nil, err
	}
	s, err := c.start(ctx, client, cmd)
	if err == nil || ctx.Err() != nil {
		return s, err
	}
	client, err = c.connect(ctx, client)
	if err != nil {
		return nil, err
	}
	return c.start(ctx, client, cmd)
}

type started struct {
	s   *cmdSession
	err error
}

// start runs startCmd until the request ends. A connection that can't
// start a command in time is closed, which also ends the startCmd waiting
// on it, and that deadline holds after the request has gone.
func (c *pipeConn) start(ctx context.Context, client *ssh.Client, cmd string) (*cmdSession, error) {
	done := make(chan started, 1)
	go func() {
		s, err := startCmd(client, cmd)
		done <- started{s, err}
	}()
	timer := time.NewTimer(c.timeout)
	select {
	case res := <-done:
		timer.Stop()
		return res.s, res.err
	case <-ctx.Done():
		go abandon(done, timer, client)
		return nil, ctx.Err()
	case <-timer.C:
		_ = client.Close()
		go closeStarted(done)
		return nil, fmt.Errorf("starting %q on pipe timed out", cmd)
	}
}

// abandon closes a session no request is waiting for, or the connection if
// the session doesn't start in time.
func abandon(done <-chan started, timer *time.Timer, client *ssh.Client) {
	select {
	case res := <-done:
		timer.Stop()
		if res.s != nil {
			_ = res.s.Close()
		}
	case <-timer.C:
		_ = client.Close()
		closeStarted(done)
	}
}

func closeStarted(done <-chan started) {
	if res := <-done; res.s != nil {
		_ = res.s.Close()
	}
}

func dialSSH(info *pipe.SSHClientInfo, timeout time.Duration) (*ssh.Client, error) {
	signer, err := clientSigner(info)
	if err != nil {
		return nil, err
	}
	var auth []ssh.AuthMethod
	if signer != nil {
		auth = append(auth, ssh.PublicKeys(signer))
	}
	addr := info.RemoteHost
	if !strings.Contains(addr, ":") {
		addr += ":22"
	}

	deadline := time.Now().Add(timeout)
	conn, err := (&net.Dialer{Deadline: deadline}).Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(deadline)
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, info.RemoteHostname, &ssh.ClientConfig{
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		User:            info.RemoteUser,
	})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(sshConn, chans, reqs), nil
}

// clientSigner loads the key, and the certificate for it if there is one.
// It returns nil when there is no key.
func clientSigner(info *pipe.SSHClientInfo) (ssh.Signer, error) {
	if info.KeyLocation == "" {
		return nil, nil
	}
	data, err := os.ReadFile(info.KeyLocation)
	if err != nil {
		return nil, err
	}
	var signer ssh.Signer
	if info.KeyPassphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(info.KeyPassphrase))
	} else {
		signer, err = ssh.ParsePrivateKey(data)
	}
	if err != nil || info.CertificateLocation == "" {
		return signer, err
	}
	certData, err := os.ReadFile(info.CertificateLocation)
	if err != nil {
		return nil, err
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(certData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		return nil, fmt.Errorf("%s is not an ssh certificate", info.CertificateLocation)
	}
	return ssh.NewCertSigner(cert, signer)
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

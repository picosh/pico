# wish middleware

This repo contains a collection of wish middleware we've built for our
services.

- [cms](#cms)
- [send](#send)
- [proxy](#proxy)

## comms

- [website](https://pico.sh)
- [irc #pico.sh](irc://irc.libera.chat/#pico.sh)
- [mailing list](https://lists.sr.ht/~erock/pico.sh)
- [ticket tracker](https://todo.sr.ht/~erock/pico.sh)
- [email](mailto:hello@pico.sh)

## cms

A content management system wish ssh app.  The goal of this library is to
provide a wish middleware that lets users ssh into this app to manage their
account as well as their posts.

### setup

You are responsible for creating your own sql tables.  A copy of the schema is
in this repo.

### example

```go
import (
  "github.com/charmbracelet/wish"
  bm "github.com/charmbracelet/wish/bubbletea"
  "github.com/gliderlabs/ssh"
  "git.sr.ht/~erock/pico/wish/cms"
  "git.sr.ht/~erock/pico/wish/cms/config"
)

type SSHServer struct{}
func (me *SSHServer) authHandler(ctx ssh.Context, key ssh.PublicKey) bool {
	return true
}

func main() {
  cfg := config.NewConfigCms()
  handler := cms.Middleware(cfg)

  sshServer := &SSHServer{}
  s, err := wish.NewServer(
    wish.WithAddress(fmt.Sprintf("%s:%s", "localhost", "2222")),
    wish.WithHostKeyPath("ssh_data/term_info_ed25519"),
    wish.WithPublicKeyAuth(sshServer.authHandler),
    wish.WithMiddleware(bm.Middleware(handler)),
  )

  // ... the rest of the wish initialization
}
```

## send

wish middleware to allow secure file transfers with scp or sftp

### example

```go
package main

import (
    "github.com/charmbracelet/wish"
    "github.com/neurosnap/lists.sh/internal/db/postgres"
    "git.sr.ht/~erock/pico/wish/send"
)

type SSHServer struct{}

func (me *SSHServer) authHandler(ctx ssh.Context, key ssh.PublicKey) bool {
    return true
}

func main() {
    host := "0.0.0.0"
    port := "2222"
    
    handler := &send.DbHandler{}
    
    dbh := postgres.NewDB()
    defer dbh.Close()
    
    sshServer := &SSHServer{}

    s, err := wish.NewServer(
        wish.WithAddress(fmt.Sprintf("%s:%s", host, port)),
        wish.WithHostKeyPath("ssh_data/term_info_ed25519"),
        wish.WithPublicKeyAuth(sshServer.authHandler),
        wish.WithMiddleware(send.Middleware(handler)),
    )
    if err != nil {
        panic(err)
    }

    done := make(chan os.Signal, 1)
    signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
    
    fmt.Printf("Starting SSH server on %s:%s\n", host, port)
    
    go func() {
        if err = s.ListenAndServe(); err != nil {
            panic(err)
        }
    }()

    <-done
    
    fmt.Println("Stopping SSH server")
    
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer func() { cancel() }()
    
    if err := s.Shutdown(ctx); err != nil {
        panic(err)
    }
}
```

## proxy

A command-based proxy middleware for your wish ssh apps. If you have ssh apps that only run on
certain ssh commands, this middleware will help you.

### example

```go
package example

import (
	"github.com/charmbracelet/wish"
	"github.com/gliderlabs/ssh"
	wp "git.sr.ht/~erock/pico/wish/proxy"
)

func router(sh ssh.Handler, s ssh.Session) []wish.Middleware {
	cmd := s.Command()
	mdw := []wish.Middleware{}

	if len(cmd) == 0 {
		mdw = append(mdw, lm.Middleware())
	} else if cmd[0] == "scp" {
		mdw = append(mdw, scp.Middleware())
	}

	return mdw
}

func main() {
	s, err := wish.NewServer(
		wish.WithAddress(fmt.Sprintf("%s:%s", host, port)),
		wish.WithHostKeyPath("ssh_data/term_info_ed25519"),
		wp.WithProxy(router),
	)
}
```

## attribution

- [Wish (middleware and SCP support)](https://github.com/charmbracelet/wish)
- [UI inspiration](https://github.com/charmbracelet/charm)
- [Go SFTP (SFTP support)](https://github.com/pkg/sftp)

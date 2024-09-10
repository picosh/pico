package pubsub

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/google/uuid"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	psub "github.com/picosh/pubsub"
	"github.com/picosh/send/send/utils"
)

func flagSet(cmdName string, sesh ssh.Session) *flag.FlagSet {
	cmd := flag.NewFlagSet(cmdName, flag.ContinueOnError)
	cmd.SetOutput(sesh)
	return cmd
}

func flagCheck(cmd *flag.FlagSet, posArg string, cmdArgs []string) bool {
	_ = cmd.Parse(cmdArgs)

	if posArg == "-h" || posArg == "--help" || posArg == "-help" {
		cmd.Usage()
		return false
	}
	return true
}

func NewTabWriter(out io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(out, 0, 0, 1, ' ', tabwriter.TabIndent)
}

func getUser(s ssh.Session, dbpool db.DB) (*db.User, error) {
	var err error
	key, err := shared.KeyText(s)
	if err != nil {
		return nil, fmt.Errorf("key not found")
	}

	user, err := dbpool.FindUserForKey(s.User(), key)
	if err != nil {
		return nil, err
	}

	if user.Name == "" {
		return nil, fmt.Errorf("must have username set")
	}

	return user, nil
}

// scope channel to user by prefixing name.
func toChannel(userName, name string) string {
	return fmt.Sprintf("%s/%s", userName, name)
}

func toPublicChannel(name string) string {
	return fmt.Sprintf("public/%s", name)
}

var helpStr = `Commands: [pub, sub, ls]

The simplest authenticated pubsub system.  Send messages through
user-defined channels.  Channels are private to the authenticated
ssh user.  The default pubsub model is multicast with bidirectional
blocking, meaning a publisher ("pub") will send its message to all
subscribers ("sub").  Further, both "pub" and "sub" will wait for
at least one event to be sent or received.`

type CliHandler struct {
	DBPool      db.DB
	Logger      *slog.Logger
	Storage     storage.StorageServe
	RegistryUrl string
	PubSub      *psub.Cfg
}

func WishMiddleware(handler *CliHandler) wish.Middleware {
	dbpool := handler.DBPool
	log := handler.Logger
	pubsub := handler.PubSub

	return func(next ssh.Handler) ssh.Handler {
		return func(sesh ssh.Session) {
			ctx := sesh.Context()
			user, err := getUser(sesh, dbpool)
			if err != nil {
				utils.ErrorHandler(sesh, err)
				return
			}

			args := sesh.Command()

			if len(args) == 0 {
				wish.Println(sesh, helpStr)
				next(sesh)
				return
			}

			cmd := strings.TrimSpace(args[0])
			if len(args) == 1 {
				if cmd == "help" {
					wish.Println(sesh, helpStr)
				} else if cmd == "ls" {
					channels := pubsub.PubSub.GetChannels(fmt.Sprintf("%s/", user.Name))

					if len(channels) == 0 {
						wish.Println(sesh, "no pubsub channels found")
					} else {
						outputData := "Channel Information\r\n"
						for _, channel := range channels {
							outputData += fmt.Sprintf("- %s:\r\n", channel.Name)
							outputData += "\tPubs:\r\n"

							channel.Pubs.Range(func(I string, J *psub.Pub) bool {
								outputData += fmt.Sprintf("\t- %s:\r\n", I)
								return true
							})

							outputData += "\tSubs:\r\n"

							channel.Subs.Range(func(I string, J *psub.Sub) bool {
								outputData += fmt.Sprintf("\t- %s:\r\n", I)
								return true
							})
						}
						_, _ = sesh.Write([]byte(outputData))
					}
				}
				next(sesh)
				return
			}

			repoName := strings.TrimSpace(args[1])
			cmdArgs := args[2:]
			log.Info(
				"imgs middleware detected command",
				"args", args,
				"cmd", cmd,
				"repoName", repoName,
				"cmdArgs", cmdArgs,
			)

			if cmd == "pub" {
				pubCmd := flagSet("pub", sesh)
				empty := pubCmd.Bool("e", false, "Send an empty message to subs")
				public := pubCmd.Bool("p", false, "Anyone can sub to this channel")
				if !flagCheck(pubCmd, repoName, cmdArgs) {
					return
				}
				channelName := repoName

				var reader io.Reader
				if *empty {
					reader = bytes.NewReader(make([]byte, 1))
				} else {
					reader = sesh
				}

				name := toChannel(user.Name, channelName)
				if *public {
					name = toPublicChannel(channelName)
				}

				wish.Println(sesh, "sending msg ...")
				pub := &psub.Pub{
					ID:     fmt.Sprintf("%s (%s@%s)", uuid.NewString(), user.Name, sesh.RemoteAddr().String()),
					Done:   make(chan struct{}),
					Reader: reader,
				}

				count := 0
				channelInfo := pubsub.PubSub.GetChannel(name)

				if channelInfo != nil {
					channelInfo.Subs.Range(func(I string, J *psub.Sub) bool {
						count++
						return true
					})
				}

				if count == 0 {
					wish.Println(sesh, "no subs found ... waiting")
				}

				go func() {
					<-ctx.Done()
					pub.Cleanup()
				}()

				err = pubsub.PubSub.Pub(name, pub)
				wish.Println(sesh, "msg sent!")
				if err != nil {
					wish.Errorln(sesh, err)
				}
			} else if cmd == "sub" {
				pubCmd := flagSet("pub", sesh)
				public := pubCmd.Bool("p", false, "Subscribe to a public channel")
				if !flagCheck(pubCmd, repoName, cmdArgs) {
					return
				}
				channelName := repoName

				name := toChannel(user.Name, channelName)
				if *public {
					name = toPublicChannel(channelName)
				}

				sub := &psub.Sub{
					ID:     fmt.Sprintf("%s (%s@%s)", uuid.NewString(), user.Name, sesh.RemoteAddr().String()),
					Writer: sesh,
					Done:   make(chan struct{}),
					Data:   make(chan []byte),
				}

				go func() {
					<-ctx.Done()
					sub.Cleanup()
				}()
				err = pubsub.PubSub.Sub(name, sub)
				if err != nil {
					wish.Errorln(sesh, err)
				}
			}

			next(sesh)
		}
	}
}

package pubsub

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/google/uuid"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
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

var helpStr = `Commands: [pub, sub, ls, pipe]

The simplest authenticated pubsub system.  Send messages through
user-defined channels.  Channels are private to the authenticated
ssh user.  The default pubsub model is multicast with bidirectional
blocking, meaning a publisher ("pub") will send its message to all
subscribers ("sub").  Further, both "pub" and "sub" will wait for
at least one event to be sent or received. Pipe ("pipe") allows
for bidirectional messages to be sent between any clients connected
to a pipe.`

type CliHandler struct {
	DBPool db.DB
	Logger *slog.Logger
	PubSub *psub.Cfg
	Cfg    *shared.ConfigSite
}

func toSshCmd(cfg *shared.ConfigSite) string {
	port := "22"
	if cfg.Port != "" {
		port = fmt.Sprintf("-p %s", cfg.Port)
	}
	return fmt.Sprintf("%s %s", port, cfg.Domain)
}

func WishMiddleware(handler *CliHandler) wish.Middleware {
	dbpool := handler.DBPool
	pubsub := handler.PubSub

	return func(next ssh.Handler) ssh.Handler {
		return func(sesh ssh.Session) {
			logger := handler.Logger
			ctx := sesh.Context()
			user, err := getUser(sesh, dbpool)
			if err != nil {
				utils.ErrorHandler(sesh, err)
				return
			}

			logger = shared.LoggerWithUser(logger, user)

			args := sesh.Command()

			if len(args) == 0 {
				wish.Println(sesh, helpStr)
				next(sesh)
				return
			}

			cmd := strings.TrimSpace(args[0])
			if cmd == "help" {
				wish.Println(sesh, helpStr)
			} else if cmd == "ls" {
				channelFilter := fmt.Sprintf("%s/", user.Name)
				if handler.DBPool.HasFeatureForUser(user.ID, "admin") {
					channelFilter = ""
				}

				channels := pubsub.PubSub.GetChannels(channelFilter)
				pipes := pubsub.PubSub.GetPipes(channelFilter)

				if len(channels) == 0 && len(pipes) == 0 {
					wish.Println(sesh, "no pubsub channels or pipes found")
				} else {
					var outputData string
					if len(channels) > 0 {
						outputData += "Channel Information\r\n"
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
					}

					if len(pipes) > 0 {
						outputData += "Pipe Information\r\n"
						for _, pipe := range pipes {
							outputData += fmt.Sprintf("- %s:\r\n", pipe.Name)
							outputData += "\tClients:\r\n"

							pipe.Clients.Range(func(I string, J *psub.PipeClient) bool {
								outputData += fmt.Sprintf("\t- %s:\r\n", I)
								return true
							})
						}
					}

					_, _ = sesh.Write([]byte(outputData))
				}
			}

			channelName := ""
			cmdArgs := args[1:]
			if len(args) > 1 {
				channelName = strings.TrimSpace(args[1])
				cmdArgs = args[2:]
			}
			logger.Info(
				"imgs middleware detected command",
				"args", args,
				"cmd", cmd,
				"channelName", channelName,
				"cmdArgs", cmdArgs,
			)

			if cmd == "pub" {
				pubCmd := flagSet("pub", sesh)
				empty := pubCmd.Bool("e", false, "Send an empty message to subs")
				public := pubCmd.Bool("p", false, "Anyone can sub to this channel")
				timeout := pubCmd.Duration("t", -1, "Timeout as a Go duration before cancelling the pub event. Valid time units are 'ns', 'us' (or 'µs'), 'ms', 's', 'm', 'h'. Default is no timeout.")
				if !flagCheck(pubCmd, channelName, cmdArgs) {
					return
				}

				var reader io.Reader
				if *empty {
					reader = bytes.NewReader(make([]byte, 1))
				} else {
					reader = sesh
				}

				if channelName == "" {
					channelName = uuid.NewString()
				}
				name := toChannel(user.Name, channelName)
				if *public {
					name = toPublicChannel(channelName)
				}
				wish.Printf(
					sesh,
					"subscribe to this channel:\n\tssh %s sub %s\n",
					toSshCmd(handler.Cfg),
					channelName,
				)

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

				tt := *timeout
				if count == 0 {
					str := "no subs found ... waiting"
					if tt > 0 {
						str += " " + tt.String()
					}
					wish.Println(sesh, str)
				}

				go func() {
					<-ctx.Done()
					pub.Cleanup()
				}()

				go func() {
					// never cancel pub event
					if tt == -1 {
						return
					}

					<-time.After(tt)
					pub.Cleanup()
					wish.Fatalln(sesh, "timeout reached, exiting ...")
				}()

				err = pubsub.PubSub.Pub(name, pub)
				wish.Println(sesh, "msg sent!")
				if err != nil {
					wish.Errorln(sesh, err)
				}
			} else if cmd == "sub" {
				pubCmd := flagSet("pub", sesh)
				public := pubCmd.Bool("p", false, "Subscribe to a public channel")
				if !flagCheck(pubCmd, channelName, cmdArgs) {
					return
				}
				channelName := channelName

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
			} else if cmd == "pipe" {
				pipeCmd := flagSet("pipe", sesh)
				public := pipeCmd.Bool("p", false, "Pipe to a public channel")
				replay := pipeCmd.Bool("r", false, "Replay messages to the client that sent it")
				if !flagCheck(pipeCmd, channelName, cmdArgs) {
					return
				}
				isCreator := channelName == ""
				if isCreator {
					channelName = uuid.NewString()
				}
				name := toChannel(user.Name, channelName)
				if *public {
					name = toPublicChannel(channelName)
				}
				if isCreator {
					wish.Printf(
						sesh,
						"subscribe to this channel:\n\tssh %s sub %s\n",
						toSshCmd(handler.Cfg),
						channelName,
					)
				}

				pipe := &psub.PipeClient{
					ID:         fmt.Sprintf("%s (%s@%s)", uuid.NewString(), user.Name, sesh.RemoteAddr().String()),
					Done:       make(chan struct{}),
					Data:       make(chan psub.PipeMessage),
					ReadWriter: sesh,
					Replay:     *replay,
				}

				go func() {
					<-ctx.Done()
					pipe.Cleanup()
				}()
				readErr, writeErr := pubsub.PubSub.Pipe(name, pipe)
				if readErr != nil {
					wish.Errorln(sesh, readErr)
				}
				if writeErr != nil {
					wish.Errorln(sesh, writeErr)
				}
			}

			next(sesh)
		}
	}
}

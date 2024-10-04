package pubsub

import (
	"bytes"
	"context"
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
	cmd.Usage = func() {
		fmt.Fprintf(cmd.Output(), "Usage: %s <topic> [args...]\nArgs:\n", cmdName)
		cmd.PrintDefaults()
	}
	return cmd
}

func flagCheck(cmd *flag.FlagSet, posArg string, cmdArgs []string) bool {
	err := cmd.Parse(cmdArgs)

	if err != nil || posArg == "help" {
		if posArg == "help" {
			cmd.Usage()
		}
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

// scope topic to user by prefixing name.
func toTopic(userName, topic string) string {
	return fmt.Sprintf("%s/%s", userName, topic)
}

func toPublicTopic(topic string) string {
	return fmt.Sprintf("public/%s", topic)
}

func clientInfo(clients []*psub.Client, clientType string) string {
	if len(clients) == 0 {
		return ""
	}

	outputData := fmt.Sprintf("\t%s:\r\n", clientType)

	for _, client := range clients {
		outputData += fmt.Sprintf("\t- %s:\r\n", client.ID)
	}

	return outputData
}

var helpStr = func(sshCmd string) string {
	return fmt.Sprintf(`Command: ssh %s <help | ls | pub | sub | pipe> <topic> [-h | args...]

The simplest authenticated pubsub system.  Send messages through
user-defined topics.  Topics are private to the authenticated
ssh user.  The default pubsub model is multicast with bidirectional
blocking, meaning a publisher ("pub") will send its message to all
subscribers ("sub").  Further, both "pub" and "sub" will wait for
at least one event to be sent or received. Pipe ("pipe") allows
for bidirectional messages to be sent between any clients connected
to a pipe.

Think of these different commands in terms of the direction the
data is being sent:

- pub => writes to client
- sub => reads from client
- pipe => read and write between clients
`, sshCmd)
}

type CliHandler struct {
	DBPool db.DB
	Logger *slog.Logger
	PubSub psub.PubSub
	Cfg    *shared.ConfigSite
}

func toSshCmd(cfg *shared.ConfigSite) string {
	port := ""
	if cfg.PortOverride != "22" {
		port = fmt.Sprintf("-p %s ", cfg.PortOverride)
	}
	return fmt.Sprintf("%s%s", port, cfg.Domain)
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
				wish.Println(sesh, helpStr(toSshCmd(handler.Cfg)))
				next(sesh)
				return
			}

			isAdmin := handler.DBPool.HasFeatureForUser(user.ID, "admin")

			cmd := strings.TrimSpace(args[0])
			if cmd == "help" {
				wish.Println(sesh, helpStr(toSshCmd(handler.Cfg)))
				next(sesh)
				return
			} else if cmd == "ls" {
				topicFilter := fmt.Sprintf("%s/", user.Name)
				if isAdmin {
					topicFilter = ""
				}

				var channels []*psub.Channel

				for topic, channel := range pubsub.GetChannels() {
					if strings.HasPrefix(topic, topicFilter) {
						channels = append(channels, channel)
					}
				}

				if len(channels) == 0 {
					wish.Println(sesh, "no pubsub channels found")
				} else {
					var outputData string
					if len(channels) > 0 {
						outputData += "Channel Information\r\n"
						for _, channel := range channels {
							outputData += fmt.Sprintf("- %s:\r\n", channel.Topic)
							outputData += "\tClients:\r\n"

							var pubs []*psub.Client
							var subs []*psub.Client
							var pipes []*psub.Client

							for _, client := range channel.GetClients() {
								if client.Direction == psub.ChannelDirectionInput {
									pubs = append(pubs, client)
								} else if client.Direction == psub.ChannelDirectionOutput {
									subs = append(subs, client)
								} else if client.Direction == psub.ChannelDirectionInputOutput {
									pipes = append(pipes, client)
								}
							}
							outputData += clientInfo(pubs, "Pubs")
							outputData += clientInfo(subs, "Subs")
							outputData += clientInfo(pipes, "Pipes")
						}
					}

					_, _ = sesh.Write([]byte(outputData))
				}

				next(sesh)
				return
			}

			topic := ""
			cmdArgs := args[1:]
			if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
				topic = strings.TrimSpace(args[1])
				cmdArgs = args[2:]
			}

			logger.Info(
				"pubsub middleware detected command",
				"args", args,
				"cmd", cmd,
				"topic", topic,
				"cmdArgs", cmdArgs,
			)

			if cmd == "pub" {
				pubCmd := flagSet("pub", sesh)
				empty := pubCmd.Bool("e", false, "Send an empty message to subs")
				public := pubCmd.Bool("p", false, "Anyone can sub to this topic")
				block := pubCmd.Bool("b", true, "Block writes until a subscriber is available")
				timeout := pubCmd.Duration("t", 30*24*time.Hour, "Timeout as a Go duration to block for a subscriber to be available. Valid time units are 'ns', 'us' (or 'µs'), 'ms', 's', 'm', 'h'. Default is 30 days.")
				if !flagCheck(pubCmd, topic, cmdArgs) {
					return
				}

				if pubCmd.NArg() == 1 && topic == "" {
					topic = pubCmd.Arg(0)
				}

				logger.Info(
					"flags parsed",
					"cmd", cmd,
					"empty", *empty,
					"public", *public,
					"block", *block,
					"timeout", *timeout,
					"topic", topic,
				)

				var rw io.ReadWriter
				if *empty {
					rw = bytes.NewBuffer(make([]byte, 1))
				} else {
					rw = sesh
				}

				if topic == "" {
					topic = uuid.NewString()
				}

				var name string

				if isAdmin && strings.HasPrefix(topic, "/") {
					name = strings.TrimPrefix(topic, "/")
				} else {
					name = toTopic(user.Name, topic)
					if *public {
						name = toPublicTopic(topic)
					}
				}

				wish.Printf(
					sesh,
					"subscribe to this channel:\n\tssh %s sub %s\n",
					toSshCmd(handler.Cfg),
					topic,
				)

				wish.Println(sesh, "sending msg ...")

				var pubCtx context.Context = ctx

				if *block {
					count := 0
					for topic, channel := range pubsub.GetChannels() {
						if topic == name {
							for _, client := range channel.GetClients() {
								if client.Direction == psub.ChannelDirectionOutput || client.Direction == psub.ChannelDirectionInputOutput {
									count++
								}
							}
							break
						}
					}

					tt := *timeout
					if count == 0 {
						termMsg := "no subs found ... waiting"
						if tt > 0 {
							termMsg += " " + tt.String()
						}
						wish.Println(sesh, termMsg)

						downCtx, cancelFunc := context.WithCancel(ctx)
						pubCtx = downCtx

						ready := make(chan struct{})

						go func() {
							for {
								select {
								case <-ctx.Done():
									cancelFunc()
									return
								default:
									count := 0
									for topic, channel := range pubsub.GetChannels() {
										if topic == name {
											for _, client := range channel.GetClients() {
												if client.Direction == psub.ChannelDirectionOutput || client.Direction == psub.ChannelDirectionInputOutput {
													count++
												}
											}
											break
										}
									}

									if count > 0 {
										close(ready)
										return
									}
								}
							}
						}()

						select {
						case <-ready:
						case <-time.After(tt):
							cancelFunc()
							wish.Fatalln(sesh, "timeout reached, exiting ...")
						}
					}
				}

				err = pubsub.Pub(
					pubCtx,
					fmt.Sprintf("%s (%s@%s)", uuid.NewString(), user.Name, sesh.RemoteAddr().String()),
					rw,
					[]*psub.Channel{
						psub.NewChannel(name),
					},
					*block,
				)

				wish.Println(sesh, "msg sent!")
				if err != nil {
					wish.Errorln(sesh, err)
				}
			} else if cmd == "sub" {
				subCmd := flagSet("sub", sesh)
				public := subCmd.Bool("p", false, "Subscribe to a public topic")
				keepAlive := subCmd.Bool("k", false, "Keep the subscription alive even after the publisher as died")
				if !flagCheck(subCmd, topic, cmdArgs) {
					return
				}

				if subCmd.NArg() == 1 && topic == "" {
					topic = subCmd.Arg(0)
				}

				logger.Info(
					"flags parsed",
					"cmd", cmd,
					"public", *public,
					"keepAlive", *keepAlive,
					"topic", topic,
				)

				var name string

				if isAdmin && strings.HasPrefix(topic, "/") {
					name = strings.TrimPrefix(topic, "/")
				} else {
					name = toTopic(user.Name, topic)
					if *public {
						name = toPublicTopic(topic)
					}
				}

				err = pubsub.Sub(
					ctx,
					fmt.Sprintf("%s (%s@%s)", uuid.NewString(), user.Name, sesh.RemoteAddr().String()),
					sesh,
					[]*psub.Channel{
						psub.NewChannel(name),
					},
					*keepAlive,
				)

				if err != nil {
					wish.Errorln(sesh, err)
				}
			} else if cmd == "pipe" {
				pipeCmd := flagSet("pipe", sesh)
				public := pipeCmd.Bool("p", false, "Pipe to a public topic")
				replay := pipeCmd.Bool("r", false, "Replay messages to the client that sent it")
				if !flagCheck(pipeCmd, topic, cmdArgs) {
					return
				}

				if pipeCmd.NArg() == 1 && topic == "" {
					topic = pipeCmd.Arg(0)
				}

				logger.Info(
					"flags parsed",
					"cmd", cmd,
					"public", *public,
					"replay", *replay,
					"topic", topic,
				)

				isCreator := topic == ""
				if isCreator {
					topic = uuid.NewString()
				}

				var name string

				if isAdmin && strings.HasPrefix(topic, "/") {
					name = strings.TrimPrefix(topic, "/")
				} else {
					name = toTopic(user.Name, topic)
					if *public {
						name = toPublicTopic(topic)
					}
				}

				if isCreator {
					wish.Printf(
						sesh,
						"subscribe to this topic:\n\tssh %s sub %s\n",
						toSshCmd(handler.Cfg),
						topic,
					)
				}

				readErr, writeErr := pubsub.Pipe(
					ctx,
					fmt.Sprintf("%s (%s@%s)", uuid.NewString(), user.Name, sesh.RemoteAddr().String()),
					sesh,
					[]*psub.Channel{
						psub.NewChannel(name),
					},
					*replay,
				)

				if readErr != nil {
					wish.Errorln(sesh, "error reading from pipe", readErr)
				}

				if writeErr != nil {
					wish.Errorln(sesh, "error writing to pipe", writeErr)
				}
			}

			next(sesh)
		}
	}
}

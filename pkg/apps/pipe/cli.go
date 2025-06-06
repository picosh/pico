package pipe

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/antoniomika/syncmap"
	"github.com/google/uuid"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/shared"
	psub "github.com/picosh/pubsub"
	gossh "golang.org/x/crypto/ssh"
)

func flagSet(cmdName string, sesh *pssh.SSHServerConnSession) *flag.FlagSet {
	cmd := flag.NewFlagSet(cmdName, flag.ContinueOnError)
	cmd.SetOutput(sesh)
	cmd.Usage = func() {
		_, _ = fmt.Fprintf(cmd.Output(), "Usage: %s <topic> [args...]\nArgs:\n", cmdName)
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

// scope topic to user by prefixing name.
func toTopic(userName, topic string) string {
	return fmt.Sprintf("%s/%s", userName, topic)
}

func toPublicTopic(topic string) string {
	return fmt.Sprintf("public/%s", topic)
}

func clientInfo(clients []*psub.Client, isAdmin bool, clientType string) string {
	if len(clients) == 0 {
		return ""
	}

	outputData := fmt.Sprintf("    %s:\r\n", clientType)

	for _, client := range clients {
		if strings.HasPrefix(client.ID, "admin-") && !isAdmin {
			continue
		}

		outputData += fmt.Sprintf("    - %s\r\n", client.ID)
	}

	return outputData
}

var helpStr = func(sshCmd string) string {
	data := fmt.Sprintf(`Command: ssh %s <help | ls | pub | sub | pipe> <topic> [-h | args...]

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

	data = strings.ReplaceAll(data, "\n", "\r\n")

	return data
}

type CliHandler struct {
	DBPool  db.DB
	Logger  *slog.Logger
	PubSub  psub.PubSub
	Cfg     *shared.ConfigSite
	Waiters *syncmap.Map[string, []string]
	Access  *syncmap.Map[string, []string]
}

func (h *CliHandler) GetLogger(s *pssh.SSHServerConnSession) *slog.Logger {
	return h.Logger
}

func toSshCmd(cfg *shared.ConfigSite) string {
	port := ""
	if cfg.PortOverride != "22" {
		port = fmt.Sprintf("-p %s ", cfg.PortOverride)
	}
	return fmt.Sprintf("%s%s", port, cfg.Domain)
}

// parseArgList parses a comma separated list of arguments.
func parseArgList(arg string) []string {
	argList := strings.Split(arg, ",")
	for i, acc := range argList {
		argList[i] = strings.TrimSpace(acc)
	}
	return argList
}

// checkAccess checks if the user has access to a topic based on an access list.
func checkAccess(accessList []string, userName string, sesh *pssh.SSHServerConnSession) bool {
	for _, acc := range accessList {
		if acc == userName {
			return true
		}

		if key := sesh.PublicKey(); key != nil && acc == gossh.FingerprintSHA256(key) {
			return true
		}
	}

	return false
}

func Middleware(handler *CliHandler) pssh.SSHServerMiddleware {
	pubsub := handler.PubSub

	return func(next pssh.SSHServerHandler) pssh.SSHServerHandler {
		return func(sesh *pssh.SSHServerConnSession) error {
			ctx := sesh.Context()
			logger := pssh.GetLogger(sesh)
			user := pssh.GetUser(sesh)

			args := sesh.Command()

			if len(args) == 0 {
				_, _ = fmt.Fprintln(sesh, helpStr(toSshCmd(handler.Cfg)))
				return next(sesh)
			}

			userName := "public"

			userNameAddition := ""

			isAdmin := false
			impersonate := false
			if user != nil {
				isAdmin = handler.DBPool.HasFeatureForUser(user.ID, "admin")
				if isAdmin && strings.HasPrefix(sesh.User(), "admin__") {
					impersonate = true
				}

				userName = user.Name
				if user.PublicKey != nil && user.PublicKey.Name != "" {
					userNameAddition = fmt.Sprintf("-%s", user.PublicKey.Name)
				}
			}

			pipeCtx, cancel := context.WithCancel(ctx)

			go func() {
				defer cancel()

				for {
					select {
					case <-pipeCtx.Done():
						return
					default:
						_, err := sesh.SendRequest("ping@pico.sh", false, nil)
						if err != nil {
							logger.Error("error sending ping", "err", err)
							return
						}

						time.Sleep(5 * time.Second)
					}
				}
			}()

			cmd := strings.TrimSpace(args[0])
			if cmd == "help" {
				_, _ = fmt.Fprintln(sesh, helpStr(toSshCmd(handler.Cfg)))
				return next(sesh)
			} else if cmd == "ls" {
				if userName == "public" {
					err := fmt.Errorf("access denied")
					sesh.Fatal(err)
					return err
				}

				topicFilter := fmt.Sprintf("%s/", userName)
				if isAdmin {
					topicFilter = ""
					if len(args) > 1 {
						topicFilter = args[1]
					}
				}

				var channels []*psub.Channel
				waitingChannels := map[string][]string{}

				for topic, channel := range pubsub.GetChannels() {
					if strings.HasPrefix(topic, topicFilter) {
						channels = append(channels, channel)
					}
				}

				for channel, clients := range handler.Waiters.Range {
					if strings.HasPrefix(channel, topicFilter) {
						waitingChannels[channel] = clients
					}
				}

				if len(channels) == 0 && len(waitingChannels) == 0 {
					_, _ = fmt.Fprintln(sesh, "no pubsub channels found")
				} else {
					var outputData string
					if len(channels) > 0 || len(waitingChannels) > 0 {
						outputData += "Channel Information\r\n"
						for _, channel := range channels {
							extraData := ""

							if accessList, ok := handler.Access.Load(channel.Topic); ok && len(accessList) > 0 {
								extraData += fmt.Sprintf(" (Access List: %s)", strings.Join(accessList, ", "))
							}

							outputData += fmt.Sprintf("- %s:%s\r\n", channel.Topic, extraData)
							outputData += "  Clients:\r\n"

							var pubs []*psub.Client
							var subs []*psub.Client
							var pipes []*psub.Client

							for _, client := range channel.GetClients() {
								switch client.Direction {
								case psub.ChannelDirectionInput:
									pubs = append(pubs, client)
								case psub.ChannelDirectionOutput:
									subs = append(subs, client)
								case psub.ChannelDirectionInputOutput:
									pipes = append(pipes, client)
								}
							}
							outputData += clientInfo(pubs, isAdmin, "Pubs")
							outputData += clientInfo(subs, isAdmin, "Subs")
							outputData += clientInfo(pipes, isAdmin, "Pipes")
						}

						for waitingChannel, channelPubs := range waitingChannels {
							extraData := ""

							if accessList, ok := handler.Access.Load(waitingChannel); ok && len(accessList) > 0 {
								extraData += fmt.Sprintf(" (Access List: %s)", strings.Join(accessList, ", "))
							}

							outputData += fmt.Sprintf("- %s:%s\r\n", waitingChannel, extraData)
							outputData += "  Clients:\r\n"
							outputData += fmt.Sprintf("    %s:\r\n", "Waiting Pubs")
							for _, client := range channelPubs {
								if strings.HasPrefix(client, "admin-") && !isAdmin {
									continue
								}
								outputData += fmt.Sprintf("    - %s\r\n", client)
							}
						}
					}

					_, _ = sesh.Write([]byte(outputData))
				}

				return next(sesh)
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

			uuidStr := uuid.NewString()
			if impersonate {
				uuidStr = fmt.Sprintf("admin-%s", uuidStr)
			}

			clientID := fmt.Sprintf("%s (%s%s@%s)", uuidStr, userName, userNameAddition, sesh.RemoteAddr().String())

			var err error

			if cmd == "pub" {
				pubCmd := flagSet("pub", sesh)
				access := pubCmd.String("a", "", "Comma separated list of pico usernames or ssh-key fingerprints to allow access to a topic")
				empty := pubCmd.Bool("e", false, "Send an empty message to subs")
				public := pubCmd.Bool("p", false, "Publish message to public topic")
				block := pubCmd.Bool("b", true, "Block writes until a subscriber is available")
				timeout := pubCmd.Duration("t", 30*24*time.Hour, "Timeout as a Go duration to block for a subscriber to be available. Valid time units are 'ns', 'us' (or 'µs'), 'ms', 's', 'm', 'h'. Default is 30 days.")
				clean := pubCmd.Bool("c", false, "Don't send status messages")

				if !flagCheck(pubCmd, topic, cmdArgs) {
					return err
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
					"access", *access,
					"clean", *clean,
				)

				var accessList []string

				if *access != "" {
					accessList = parseArgList(*access)
				}

				var rw io.ReadWriter
				if *empty {
					rw = bytes.NewBuffer(make([]byte, 1))
				} else {
					rw = sesh
				}

				if topic == "" {
					topic = uuid.NewString()
				}

				var withoutUser string
				var name string
				msgFlag := ""

				if isAdmin && strings.HasPrefix(topic, "/") {
					name = strings.TrimPrefix(topic, "/")
				} else {
					name = toTopic(userName, topic)
					if *public {
						name = toPublicTopic(topic)
						msgFlag = "-p "
						withoutUser = name
					} else {
						withoutUser = topic
					}
				}

				var accessListCreator bool

				_, loaded := handler.Access.LoadOrStore(name, accessList)
				if !loaded {
					defer func() {
						handler.Access.Delete(name)
					}()

					accessListCreator = true
				}

				if accessList, ok := handler.Access.Load(withoutUser); ok && len(accessList) > 0 && !isAdmin {
					if checkAccess(accessList, userName, sesh) || accessListCreator {
						name = withoutUser
					} else if !*public {
						name = toTopic(userName, withoutUser)
					} else {
						topic = uuid.NewString()
						name = toPublicTopic(topic)
					}
				}

				if !*clean {
					_, _ = fmt.Fprintf(
						sesh,
						"subscribe to this channel:\n  ssh %s sub %s%s\n",
						toSshCmd(handler.Cfg),
						msgFlag,
						topic,
					)
				}

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
						currentWaiters, _ := handler.Waiters.LoadOrStore(name, nil)
						handler.Waiters.Store(name, append(currentWaiters, clientID))

						termMsg := "no subs found ... waiting"
						if tt > 0 {
							termMsg += " " + tt.String()
						}

						if !*clean {
							_, _ = fmt.Fprintln(sesh, termMsg)
						}

						ready := make(chan struct{})

						go func() {
							for {
								select {
								case <-pipeCtx.Done():
									cancel()
									return
								case <-time.After(1 * time.Millisecond):
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
						case <-pipeCtx.Done():
						case <-time.After(tt):
							cancel()

							if !*clean {
								sesh.Fatal(fmt.Errorf("timeout reached, exiting"))
							} else {
								err = sesh.Exit(1)
								if err != nil {
									logger.Error("error exiting session", "err", err)
								}

								_ = sesh.Close()
							}
						}

						newWaiters, _ := handler.Waiters.LoadOrStore(name, nil)
						newWaiters = slices.DeleteFunc(newWaiters, func(cl string) bool {
							return cl == clientID
						})
						handler.Waiters.Store(name, newWaiters)

						var toDelete []string

						for channel, clients := range handler.Waiters.Range {
							if len(clients) == 0 {
								toDelete = append(toDelete, channel)
							}
						}

						for _, channel := range toDelete {
							handler.Waiters.Delete(channel)
						}
					}
				}

				if !*clean {
					_, _ = fmt.Fprintln(sesh, "sending msg ...")
				}

				err = pubsub.Pub(
					pipeCtx,
					clientID,
					rw,
					[]*psub.Channel{
						psub.NewChannel(name),
					},
					*block,
				)

				if !*clean {
					_, _ = fmt.Fprintln(sesh, "msg sent!")
				}

				if err != nil && !*clean {
					_, _ = fmt.Fprintln(sesh.Stderr(), err)
				}
			} else if cmd == "sub" {
				subCmd := flagSet("sub", sesh)
				access := subCmd.String("a", "", "Comma separated list of pico usernames or ssh-key fingerprints to allow access to a topic")
				public := subCmd.Bool("p", false, "Subscribe to a public topic")
				keepAlive := subCmd.Bool("k", false, "Keep the subscription alive even after the publisher has died")
				clean := subCmd.Bool("c", false, "Don't send status messages")

				if !flagCheck(subCmd, topic, cmdArgs) {
					return err
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
					"clean", *clean,
					"access", *access,
				)

				var accessList []string

				if *access != "" {
					accessList = parseArgList(*access)
				}

				var withoutUser string
				var name string

				if isAdmin && strings.HasPrefix(topic, "/") {
					name = strings.TrimPrefix(topic, "/")
				} else {
					name = toTopic(userName, topic)
					if *public {
						name = toPublicTopic(topic)
						withoutUser = name
					} else {
						withoutUser = topic
					}
				}

				var accessListCreator bool

				_, loaded := handler.Access.LoadOrStore(name, accessList)
				if !loaded {
					defer func() {
						handler.Access.Delete(name)
					}()

					accessListCreator = true
				}

				if accessList, ok := handler.Access.Load(withoutUser); ok && len(accessList) > 0 && !isAdmin {
					if checkAccess(accessList, userName, sesh) || accessListCreator {
						name = withoutUser
					} else if !*public {
						name = toTopic(userName, withoutUser)
					} else {
						_, _ = fmt.Fprintln(sesh.Stderr(), "access denied")
						return err
					}
				}

				err = pubsub.Sub(
					pipeCtx,
					clientID,
					sesh,
					[]*psub.Channel{
						psub.NewChannel(name),
					},
					*keepAlive,
				)

				if err != nil && !*clean {
					_, _ = fmt.Fprintln(sesh.Stderr(), err)
				}
			} else if cmd == "pipe" {
				pipeCmd := flagSet("pipe", sesh)
				access := pipeCmd.String("a", "", "Comma separated list of pico usernames or ssh-key fingerprints to allow access to a topic")
				public := pipeCmd.Bool("p", false, "Pipe to a public topic")
				replay := pipeCmd.Bool("r", false, "Replay messages to the client that sent it")
				clean := pipeCmd.Bool("c", false, "Don't send status messages")

				if !flagCheck(pipeCmd, topic, cmdArgs) {
					return err
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
					"access", *access,
					"clean", *clean,
				)

				var accessList []string

				if *access != "" {
					accessList = parseArgList(*access)
				}

				isCreator := topic == ""
				if isCreator {
					topic = uuid.NewString()
				}

				var withoutUser string
				var name string
				flagMsg := ""

				if isAdmin && strings.HasPrefix(topic, "/") {
					name = strings.TrimPrefix(topic, "/")
				} else {
					name = toTopic(userName, topic)
					if *public {
						name = toPublicTopic(topic)
						flagMsg = "-p "
						withoutUser = name
					} else {
						withoutUser = topic
					}
				}

				var accessListCreator bool

				_, loaded := handler.Access.LoadOrStore(name, accessList)
				if !loaded {
					defer func() {
						handler.Access.Delete(name)
					}()

					accessListCreator = true
				}

				if accessList, ok := handler.Access.Load(withoutUser); ok && len(accessList) > 0 && !isAdmin {
					if checkAccess(accessList, userName, sesh) || accessListCreator {
						name = withoutUser
					} else if !*public {
						name = toTopic(userName, withoutUser)
					} else {
						topic = uuid.NewString()
						name = toPublicTopic(topic)
					}
				}

				if isCreator && !*clean {
					_, _ = fmt.Fprintf(
						sesh,
						"subscribe to this topic:\n  ssh %s sub %s%s\n",
						toSshCmd(handler.Cfg),
						flagMsg,
						topic,
					)
				}

				readErr, writeErr := pubsub.Pipe(
					pipeCtx,
					clientID,
					sesh,
					[]*psub.Channel{
						psub.NewChannel(name),
					},
					*replay,
				)

				if readErr != nil && !*clean {
					_, _ = fmt.Fprintln(sesh.Stderr(), "error reading from pipe", readErr)
				}

				if writeErr != nil && !*clean {
					_, _ = fmt.Fprintln(sesh.Stderr(), "error writing to pipe", writeErr)
				}
			}

			return next(sesh)
		}
	}
}

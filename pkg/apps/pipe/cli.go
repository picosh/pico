package pipe

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"text/tabwriter"
	"time"

	"github.com/antoniomika/syncmap"
	"github.com/google/uuid"
	"github.com/gorilla/feeds"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/pssh"
	psub "github.com/picosh/pico/pkg/pubsub"
	"github.com/picosh/pico/pkg/shared"
	gossh "golang.org/x/crypto/ssh"
)

func Middleware(handler *CliHandler) pssh.SSHServerMiddleware {
	return func(next pssh.SSHServerHandler) pssh.SSHServerHandler {
		return func(sesh *pssh.SSHServerConnSession) error {
			ctx := sesh.Context()
			logger := pssh.GetLogger(sesh)
			user := pssh.GetUser(sesh)

			args := sesh.Command()
			if len(args) == 0 {
				help(handler.Cfg, sesh)
				return next(sesh)
			}

			userName := "public"
			userNameAddition := ""
			uuidStr := uuid.NewString()
			isAdmin := false
			if user != nil {
				isAdmin = handler.DBPool.HasFeatureByUser(user.ID, "admin")
				if isAdmin && strings.HasPrefix(sesh.User(), "admin__") {
					uuidStr = fmt.Sprintf("admin-%s", uuidStr)
				}

				userName = user.Name
				if user.PublicKey != nil && user.PublicKey.Name != "" {
					addition := user.PublicKey.Name
					identity := sesh.Permissions().Extensions["identity"]
					if identity != "" && identity != "pubkey" {
						addition = identity
					}
					userNameAddition = fmt.Sprintf("-%s", addition)
				}
			}

			pipeCtx, cancel := context.WithCancel(ctx)

			cliCmd := &CliCmd{
				sesh:     sesh,
				args:     args,
				userName: userName,
				isAdmin:  isAdmin,
				pipeCtx:  pipeCtx,
				cancel:   cancel,
				user:     user,
			}

			cmd := strings.TrimSpace(args[0])
			switch cmd {
			case "help":
				help(handler.Cfg, sesh)
				return next(sesh)
			case "ls":
				err := handler.ls(cliCmd)
				if err != nil {
					logger.Error("ls cmd", "err", err)
					sesh.Fatal(err)
				}
				return next(sesh)
			case "monitor":
				err := handler.monitor(cliCmd, user)
				if err != nil {
					logger.Error("monitor cmd", "err", err)
					sesh.Fatal(err)
				}
				return next(sesh)
			case "status":
				err := handler.status(cliCmd, user)
				if err != nil {
					logger.Error("status cmd", "err", err)
					sesh.Fatal(err)
				}
				return next(sesh)
			case "rss":
				rss, err := MonitorRss(handler.DBPool, user, handler.Cfg.Domain)
				_, _ = fmt.Fprintln(sesh, rss)
				if err != nil {
					logger.Error("rss cmd", "err", err)
					sesh.Fatal(err)
				}
				return next(sesh)
			}

			topic := ""
			cmdArgs := args[1:]
			if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
				topic = strings.TrimSpace(args[1])
				cmdArgs = args[2:]
			}
			// sub commands after this line expect clipped args
			cliCmd.args = cmdArgs

			logger.Info(
				"pubsub middleware detected command",
				"args", args,
				"cmd", cmd,
				"topic", topic,
				"cmdArgs", cmdArgs,
			)

			clientID := fmt.Sprintf(
				"%s (%s%s@%s)",
				uuidStr,
				userName,
				userNameAddition,
				sesh.RemoteAddr().String(),
			)

			go func() {
				defer cancel()

				ticker := time.NewTicker(5 * time.Second)
				defer ticker.Stop()

				for {
					select {
					case <-pipeCtx.Done():
						return
					case <-ticker.C:
						_, err := sesh.SendRequest("ping@pico.sh", false, nil)
						if err != nil {
							logger.Error("error sending ping", "err", err)
							return
						}
					}
				}
			}()

			switch cmd {
			case "pub":
				err := handler.pub(cliCmd, topic, clientID)
				if err != nil {
					logger.Error("pub cmd", "err", err)
					sesh.Fatal(err)
				}
			case "sub":
				err := handler.sub(cliCmd, topic, clientID)
				if err != nil {
					logger.Error("sub cmd", "err", err)
					sesh.Fatal(err)
				}
			case "pipe":
				err := handler.pipe(cliCmd, topic, clientID)
				if err != nil {
					logger.Error("pipe cmd", "err", err)
					sesh.Fatal(err)
				}
			case "uptime":
				err := handler.uptime(cliCmd, topic, user)
				if err != nil {
					logger.Error("uptime cmd", "err", err)
					sesh.Fatal(err)
				}
			}

			return next(sesh)
		}
	}
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

type CliCmd struct {
	sesh     *pssh.SSHServerConnSession
	args     []string
	userName string
	isAdmin  bool
	pipeCtx  context.Context
	cancel   context.CancelFunc
	user     *db.User
}

func help(cfg *shared.ConfigSite, sesh *pssh.SSHServerConnSession) {
	data := fmt.Sprintf(`Command: ssh %s <command> [args...]

The simplest authenticated pubsub system.  Send messages through
user-defined topics.  Topics are private to the authenticated
ssh user.  The default pubsub model is multicast with bidirectional
blocking, meaning a publisher ("pub") will send its message to all
subscribers ("sub").  Further, both "pub" and "sub" will wait for
at least one event to be sent or received. Pipe ("pipe") allows
for bidirectional messages to be sent between any clients connected
to a pipe.

Commands:
  help                        Show this help message
  ls                          List active pubsub channels
  pub <topic> [flags]         Publish messages to a topic
  sub <topic> [flags]         Subscribe to messages from a topic
  pipe <topic> [flags]        Bidirectional messaging between clients

Monitoring commands:
  monitor <topic> <duration>  Create/update a health monitor for a topic
  monitor <topic> -d          Delete a monitor
  status                      Show health status of all monitors
  uptime                      Show uptime for a topic
  rss                         Get RSS feed of monitor alerts

Use "ssh %s <command> -h" for help on a specific command.
`, toSshCmd(cfg), toSshCmd(cfg))

	data = strings.ReplaceAll(data, "\n", "\r\n")
	_, _ = fmt.Fprintln(sesh, data)
}

func (handler *CliHandler) ls(cmd *CliCmd) error {
	if cmd.userName == "public" {
		err := fmt.Errorf("access denied")
		return err
	}

	topicFilter := fmt.Sprintf("%s/", cmd.userName)
	if cmd.isAdmin {
		topicFilter = ""
		if len(cmd.args) > 1 {
			topicFilter = cmd.args[1]
		}
	}

	var channels []*psub.Channel
	waitingChannels := map[string][]string{}

	for topic, channel := range handler.PubSub.GetChannels() {
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
		_, _ = fmt.Fprintln(cmd.sesh, "no pubsub channels found")
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
				outputData += clientInfo(pubs, cmd.isAdmin, "Pubs")
				outputData += clientInfo(subs, cmd.isAdmin, "Subs")
				outputData += clientInfo(pipes, cmd.isAdmin, "Pipes")
			}

			for waitingChannel, channelPubs := range waitingChannels {
				extraData := ""

				if accessList, ok := handler.Access.Load(waitingChannel); ok && len(accessList) > 0 {
					extraData += fmt.Sprintf(" (Access List: %s)", strings.Join(accessList, ", "))
				}

				outputData += fmt.Sprintf("- %s:%s\r\n", waitingChannel, extraData)
				outputData += fmt.Sprintf("  %s:\r\n", "Waiting Pubs")
				for _, client := range channelPubs {
					if strings.HasPrefix(client, "admin-") && !cmd.isAdmin {
						continue
					}
					outputData += fmt.Sprintf("  - %s\r\n", client)
				}
			}
		}

		_, _ = cmd.sesh.Write([]byte(outputData))
	}

	return nil
}

func (handler *CliHandler) monitor(cmd *CliCmd, user *db.User) error {
	if user == nil {
		return fmt.Errorf("access denied")
	}

	args := cmd.sesh.Command()
	topic := ""
	cmdArgs := args[1:]
	if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		topic = strings.TrimSpace(args[1])
		cmdArgs = args[2:]
	}

	monitorCmd := flagSet("monitor", cmd.sesh)
	del := monitorCmd.Bool("d", false, "Delete the monitor")

	if !flagCheck(monitorCmd, topic, cmdArgs) {
		return nil
	}

	if topic == "" {
		_, _ = fmt.Fprintln(cmd.sesh, "Usage: monitor <topic> <duration>")
		_, _ = fmt.Fprintln(cmd.sesh, "       monitor <topic> -d")
		return fmt.Errorf("topic is required")
	}

	// Resolve to fully qualified topic name
	result := resolveTopic(TopicResolveInput{
		UserName: cmd.userName,
		Topic:    topic,
		IsAdmin:  cmd.isAdmin,
		IsPublic: false,
	})
	resolvedTopic := result.Name

	if *del {
		handler.Logger.Info("removing pipe monitor", "topic", resolvedTopic)
		err := handler.DBPool.RemovePipeMonitor(user.ID, resolvedTopic)
		if err != nil {
			return fmt.Errorf("failed to delete monitor: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.sesh, "monitor deleted: %s\r\n", resolvedTopic)
		return nil
	}

	// Create/update monitor - need duration argument
	durStr := ""
	if monitorCmd.NArg() > 0 {
		durStr = monitorCmd.Arg(0)
	} else if len(cmdArgs) > 0 {
		durStr = cmdArgs[0]
	}

	if durStr == "" {
		_, _ = fmt.Fprintln(cmd.sesh, "Usage: monitor <topic> <duration>")
		return fmt.Errorf("duration is required")
	}

	dur, err := time.ParseDuration(durStr)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", durStr, err)
	}

	winEnd := time.Now().UTC().Add(dur)
	handler.Logger.Info(
		"upserting pipe monitor",
		"topic", resolvedTopic,
		"dur", dur,
		"window", winEnd.UTC().Format(time.RFC3339),
	)
	err = handler.DBPool.UpsertPipeMonitor(user.ID, resolvedTopic, dur, &winEnd)
	if err != nil {
		return fmt.Errorf("failed to create monitor: %w", err)
	}

	_, _ = fmt.Fprintf(cmd.sesh, "monitor created: %s (window: %s)\r\n", resolvedTopic, dur)
	return nil
}

func (handler *CliHandler) status(cmd *CliCmd, user *db.User) error {
	if user == nil {
		return fmt.Errorf("access denied")
	}

	monitors, err := handler.DBPool.FindPipeMonitorsByUser(user.ID)
	if err != nil {
		return fmt.Errorf("failed to fetch monitors: %w", err)
	}

	if len(monitors) == 0 {
		_, _ = fmt.Fprintln(cmd.sesh, "no monitors found")
		return nil
	}

	writer := tabwriter.NewWriter(cmd.sesh, 0, 0, 2, ' ', tabwriter.TabIndent)
	_, _ = fmt.Fprintln(writer, "Topic\tStatus\tWindow\tLast Ping\tWindow End\tReason")

	for _, m := range monitors {
		status := "healthy"
		reason := ""
		if err := m.Status(); err != nil {
			status = "unhealthy"
			reason = err.Error()
		}

		lastPing := "never"
		if m.LastPing != nil {
			lastPing = m.LastPing.UTC().Format(time.RFC3339)
		}

		windowEnd := ""
		if m.WindowEnd != nil {
			windowEnd = m.WindowEnd.UTC().Format(time.RFC3339)
		}

		_, _ = fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%s\t%s\r\n",
			m.Topic,
			status,
			m.WindowDur.String(),
			lastPing,
			windowEnd,
			reason,
		)
	}
	_ = writer.Flush()
	return nil
}

func (handler *CliHandler) uptime(cmd *CliCmd, topic string, user *db.User) error {
	if user == nil {
		return fmt.Errorf("access denied")
	}

	if topic == "" {
		_, _ = fmt.Fprintln(cmd.sesh, "usage: uptime <topic> [--from <time>] [--to <time>]")
		_, _ = fmt.Fprintln(cmd.sesh, "  --from: start time (RFC3339 or duration like '24h', '7d', default: 24h)")
		_, _ = fmt.Fprintln(cmd.sesh, "  --to: end time (RFC3339, default: now)")
		return nil
	}

	fs := flag.NewFlagSet("uptime", flag.ContinueOnError)
	fs.SetOutput(cmd.sesh)
	fromStr := fs.String("from", "", "start time (RFC3339 or duration like '24h', '7d')")
	toStr := fs.String("to", "", "end time (RFC3339, defaults to now)")

	if err := fs.Parse(cmd.args); err != nil {
		return nil
	}

	topicResult := resolveTopic(TopicResolveInput{
		UserName: cmd.userName,
		Topic:    topic,
		IsAdmin:  cmd.isAdmin,
		IsPublic: false,
	})
	resolvedTopic := topicResult.Name

	monitor, err := handler.DBPool.FindPipeMonitorByTopic(user.ID, resolvedTopic)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("monitor not found: %s", topic)
		}
		return fmt.Errorf("failed to find monitor: %w", err)
	}

	now := time.Now().UTC()
	to := now
	from := now.Add(-24 * time.Hour)

	if *fromStr != "" {
		if parsed, err := time.Parse(time.RFC3339, *fromStr); err == nil {
			from = parsed.UTC()
		} else if dur, err := parseDuration(*fromStr); err == nil {
			from = now.Add(-dur)
		} else {
			return fmt.Errorf("invalid --from value: %s", *fromStr)
		}
	}

	if *toStr != "" {
		if parsed, err := time.Parse(time.RFC3339, *toStr); err == nil {
			to = parsed.UTC()
		} else {
			return fmt.Errorf("invalid --to value: %s", *toStr)
		}
	}

	history, err := handler.DBPool.FindPipeMonitorHistory(monitor.ID, from, to)
	if err != nil {
		return fmt.Errorf("failed to fetch history: %w", err)
	}

	result := db.ComputeUptime(history, from, to)

	_, _ = fmt.Fprintf(cmd.sesh, "Monitor: %s\r\n", topic)
	_, _ = fmt.Fprintf(cmd.sesh, "Period: %s to %s\r\n", from.Format(time.RFC3339), to.Format(time.RFC3339))
	_, _ = fmt.Fprintf(cmd.sesh, "Total Duration: %s\r\n", result.TotalDuration.Round(time.Second))
	_, _ = fmt.Fprintf(cmd.sesh, "Uptime Duration: %s\r\n", result.UptimeDuration.Round(time.Second))
	_, _ = fmt.Fprintf(cmd.sesh, "Uptime: %.2f%%\r\n", result.UptimePercent)

	return nil
}

func parseDuration(s string) (time.Duration, error) {
	if len(s) == 0 {
		return 0, fmt.Errorf("empty duration")
	}
	last := s[len(s)-1]
	if last == 'd' {
		var n int
		_, err := fmt.Sscanf(s, "%d", &n)
		if err != nil {
			return 0, fmt.Errorf("invalid duration: %s", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func MonitorRss(dbpool db.DB, user *db.User, domain string) (string, error) {
	if user == nil {
		return "", fmt.Errorf("access denied")
	}

	monitors, err := dbpool.FindPipeMonitorsByUser(user.ID)
	if err != nil {
		return "", fmt.Errorf("failed to fetch monitors: %w", err)
	}

	now := time.Now()
	feed := &feeds.Feed{
		Title:       fmt.Sprintf("Pipe Monitors for %s", user.Name),
		Link:        &feeds.Link{Href: fmt.Sprintf("https://%s", domain)},
		Description: "Alerts for pipe monitor status changes",
		Author:      &feeds.Author{Name: user.Name},
		Created:     now,
	}

	var feedItems []*feeds.Item
	for _, m := range monitors {
		if err := m.Status(); err != nil {
			item := &feeds.Item{
				Id:          fmt.Sprintf("%s-%s-%d", user.ID, m.Topic, now.Unix()),
				Title:       fmt.Sprintf("ALERT: %s is unhealthy", m.Topic),
				Link:        &feeds.Link{Href: fmt.Sprintf("https://%s", domain)},
				Description: err.Error(),
				Created:     now,
				Updated:     now,
				Author:      &feeds.Author{Name: user.Name},
			}
			feedItems = append(feedItems, item)
		}
	}
	feed.Items = feedItems

	rss, err := feed.ToRss()
	if err != nil {
		return "", fmt.Errorf("failed to generate RSS: %w", err)
	}

	return rss, nil
}

func (handler *CliHandler) pub(cmd *CliCmd, topic string, clientID string) error {
	pubCmd := flagSet("pub", cmd.sesh)
	access := pubCmd.String("a", "", "Comma separated list of pico usernames or ssh-key fingerprints to allow access to a topic")
	empty := pubCmd.Bool("e", false, "Send an empty message to subs")
	public := pubCmd.Bool("p", false, "Publish message to public topic")
	block := pubCmd.Bool("b", true, "Block writes until a subscriber is available")
	timeout := pubCmd.Duration("t", 30*24*time.Hour, "Timeout as a Go duration to block for a subscriber to be available. Valid time units are 'ns', 'us' (or 'µs'), 'ms', 's', 'm', 'h'. Default is 30 days.")
	clean := pubCmd.Bool("c", false, "Don't send status messages")
	dispatcher := pubCmd.String("d", "multicast", "Type of dispatcher (e.g. multicast, round_robin)")

	if !flagCheck(pubCmd, topic, cmd.args) {
		return fmt.Errorf("invalid cmd args")
	}

	if pubCmd.NArg() == 1 && topic == "" {
		topic = pubCmd.Arg(0)
	}

	handler.Logger.Info(
		"flags parsed",
		"cmd", "pub",
		"empty", *empty,
		"public", *public,
		"block", *block,
		"timeout", *timeout,
		"topic", topic,
		"access", *access,
		"clean", *clean,
		"dispatcher", *dispatcher,
	)

	var accessList []string

	if *access != "" {
		accessList = parseArgList(*access)
	}

	var rw io.ReadWriter
	if *empty {
		rw = bytes.NewBuffer(make([]byte, 1))
	} else {
		rw = cmd.sesh
	}

	if topic == "" {
		topic = uuid.NewString()
	}

	msgFlag := ""
	if *public {
		msgFlag = "-p "
	}

	// Initial resolution to get the topic name for access storage
	initialResult := resolveTopic(TopicResolveInput{
		UserName: cmd.userName,
		Topic:    topic,
		IsAdmin:  cmd.isAdmin,
		IsPublic: *public,
	})
	name := initialResult.Name

	var accessListCreator bool
	_, loaded := handler.Access.LoadOrStore(name, accessList)
	if !loaded {
		defer func() {
			handler.Access.Delete(name)
		}()
		accessListCreator = true
	}

	// Check for existing access list and resolve final topic name
	existingAccessList, hasExistingAccess := handler.Access.Load(initialResult.WithoutUser)
	result := resolveTopic(TopicResolveInput{
		UserName:           cmd.userName,
		Topic:              topic,
		IsAdmin:            cmd.isAdmin,
		IsPublic:           *public,
		ExistingAccessList: existingAccessList,
		HasExistingAccess:  hasExistingAccess,
		IsAccessCreator:    accessListCreator,
		HasUserAccess:      checkAccess(existingAccessList, cmd.userName, cmd.sesh),
	})
	name = result.Name

	if result.GenerateNewTopic {
		topic = uuid.NewString()
		name = toPublicTopic(topic)
	}

	if !*clean {
		fmtTopic := topic
		if *access != "" {
			fmtTopic = fmt.Sprintf("%s/%s", cmd.userName, topic)
		}

		_, _ = fmt.Fprintf(
			cmd.sesh,
			"subscribe to this channel:\n  ssh %s sub %s%s\n",
			toSshCmd(handler.Cfg),
			msgFlag,
			fmtTopic,
		)
	}

	if *block {
		count := 0
		for topic, channel := range handler.PubSub.GetChannels() {
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
				_, _ = fmt.Fprintln(cmd.sesh, termMsg)
			}

			ready := make(chan struct{})

			go func() {
				for {
					select {
					case <-cmd.pipeCtx.Done():
						cmd.cancel()
						return
					case <-time.After(1 * time.Millisecond):
						count := 0
						for topic, channel := range handler.PubSub.GetChannels() {
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
			case <-cmd.pipeCtx.Done():
			case <-time.After(tt):
				cmd.cancel()

				if !*clean {
					return fmt.Errorf("timeout reached, exiting")
				} else {
					err := cmd.sesh.Exit(1)
					if err != nil {
						handler.Logger.Error("error exiting session", "err", err)
					}

					_ = cmd.sesh.Close()
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
		_, _ = fmt.Fprintln(cmd.sesh, "sending msg ...")
	}

	throttledRW := newThrottledMonitorRW(rw, handler, cmd, name)

	var dsp psub.MessageDispatcher
	dsp = &psub.MulticastDispatcher{}
	if *dispatcher == "round_robin" {
		dsp = &psub.RoundRobinDispatcher{}
	}
	channel := psub.NewChannel(name)
	_ = handler.PubSub.SetDispatcher(dsp, []*psub.Channel{channel})

	err := handler.PubSub.Pub(
		cmd.pipeCtx,
		clientID,
		throttledRW,
		[]*psub.Channel{channel},
		*block,
	)

	if !*clean {
		_, _ = fmt.Fprintln(cmd.sesh, "msg sent!")
	}

	if err != nil && !*clean {
		return err
	}

	handler.updateMonitor(cmd, name)

	return nil
}

func (handler *CliHandler) updateMonitor(cmd *CliCmd, topic string) {
	if cmd.user == nil {
		return
	}

	handler.Logger.Info("update monitor", "topic", topic)
	monitor, err := handler.DBPool.FindPipeMonitorByTopic(cmd.user.ID, topic)
	if err != nil || monitor == nil {
		handler.Logger.Info("no monitor found", "topic", topic)
		return
	}

	now := time.Now().UTC()

	// Fixed window semantics: windows are discrete, non-overlapping time slots.
	// - last_ping: always updated to show most recent activity (user visibility)
	// - window_end: only advances when current time exceeds it (health scheduling)

	// If we're past the current window, advance to the window containing `now`
	newWindowEnd := *monitor.WindowEnd
	if !now.Before(*monitor.WindowEnd) {
		// Record history for the completed window before advancing
		// This captures that the old window was healthy (had activity)
		if err := handler.DBPool.InsertPipeMonitorHistory(monitor.ID, monitor.WindowDur, monitor.WindowEnd, monitor.LastPing); err != nil {
			handler.Logger.Error("failed to insert monitor history", "err", err, "topic", topic)
		}

		// Calculate which window period `now` falls into
		elapsed := now.Sub(*monitor.WindowEnd)
		periods := int(elapsed/monitor.WindowDur) + 1
		newWindowEnd = monitor.WindowEnd.Add(time.Duration(periods) * monitor.WindowDur)

		if err := handler.DBPool.UpsertPipeMonitor(cmd.user.ID, topic, monitor.WindowDur, &newWindowEnd); err != nil {
			handler.Logger.Error("failed to advance monitor window", "err", err, "topic", topic)
		}
		handler.Logger.Info("advanced monitor window",
			"topic", topic,
			"oldWindowEnd", monitor.WindowEnd.Format(time.RFC3339),
			"newWindowEnd", newWindowEnd.Format(time.RFC3339),
			"periodsMissed", periods-1,
		)
	}

	// Always record the latest ping for user visibility
	if err := handler.DBPool.UpdatePipeMonitorLastPing(cmd.user.ID, topic, &now); err != nil {
		handler.Logger.Error("failed to update monitor last_ping", "err", err, "topic", topic)
	}

	handler.Logger.Info("recorded monitor ping",
		"topic", topic,
		"pingTime", now.Format(time.RFC3339),
		"windowEnd", newWindowEnd.Format(time.RFC3339),
	)
}

const monitorThrottleInterval = 15 * time.Second

type throttledMonitorRW struct {
	rw       io.ReadWriter
	handler  *CliHandler
	cmd      *CliCmd
	topic    string
	lastPing atomic.Int64 // Unix nanoseconds
}

func newThrottledMonitorRW(rw io.ReadWriter, handler *CliHandler, cmd *CliCmd, topic string) *throttledMonitorRW {
	return &throttledMonitorRW{
		rw:      rw,
		handler: handler,
		cmd:     cmd,
		topic:   topic,
	}
}

func (t *throttledMonitorRW) throttledUpdate() {
	now := time.Now().UnixNano()
	last := t.lastPing.Load()

	// First ping (last == 0) or interval elapsed
	if last == 0 || now-last >= int64(monitorThrottleInterval) {
		if t.lastPing.CompareAndSwap(last, now) {
			t.handler.updateMonitor(t.cmd, t.topic)
		}
	}
}

func (t *throttledMonitorRW) Read(p []byte) (int, error) {
	n, err := t.rw.Read(p)
	if n > 0 {
		t.throttledUpdate()
	}
	return n, err
}

func (t *throttledMonitorRW) Write(p []byte) (int, error) {
	n, err := t.rw.Write(p)
	if n > 0 {
		t.throttledUpdate()
	}
	return n, err
}

func (handler *CliHandler) sub(cmd *CliCmd, topic string, clientID string) error {
	subCmd := flagSet("sub", cmd.sesh)
	access := subCmd.String("a", "", "Comma separated list of pico usernames or ssh-key fingerprints to allow access to a topic")
	public := subCmd.Bool("p", false, "Subscribe to a public topic")
	keepAlive := subCmd.Bool("k", false, "Keep the subscription alive even after the publisher has died")
	clean := subCmd.Bool("c", false, "Don't send status messages")

	if !flagCheck(subCmd, topic, cmd.args) {
		return fmt.Errorf("invalid cmd args")
	}

	if subCmd.NArg() == 1 && topic == "" {
		topic = subCmd.Arg(0)
	}

	handler.Logger.Info(
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

	// Initial resolution to get the topic name for access storage
	initialResult := resolveTopic(TopicResolveInput{
		UserName: cmd.userName,
		Topic:    topic,
		IsAdmin:  cmd.isAdmin,
		IsPublic: *public,
	})
	name := initialResult.Name

	var accessListCreator bool

	_, loaded := handler.Access.LoadOrStore(name, accessList)
	if !loaded {
		defer func() {
			handler.Access.Delete(name)
		}()
		accessListCreator = true
	}

	// Check for existing access list and resolve final topic name
	existingAccessList, hasExistingAccess := handler.Access.Load(initialResult.WithoutUser)
	result := resolveTopic(TopicResolveInput{
		UserName:           cmd.userName,
		Topic:              topic,
		IsAdmin:            cmd.isAdmin,
		IsPublic:           *public,
		ExistingAccessList: existingAccessList,
		HasExistingAccess:  hasExistingAccess,
		IsAccessCreator:    accessListCreator,
		HasUserAccess:      checkAccess(existingAccessList, cmd.userName, cmd.sesh),
	})
	name = result.Name

	if result.AccessDenied {
		return fmt.Errorf("access denied")
	}

	err := handler.PubSub.Sub(
		cmd.pipeCtx,
		clientID,
		cmd.sesh,
		[]*psub.Channel{
			psub.NewChannel(name),
		},
		*keepAlive,
	)

	if err != nil && !*clean {
		return err
	}

	return nil
}

func (handler *CliHandler) pipe(cmd *CliCmd, topic string, clientID string) error {
	pipeCmd := flagSet("pipe", cmd.sesh)
	access := pipeCmd.String("a", "", "Comma separated list of pico usernames or ssh-key fingerprints to allow access to a topic")
	public := pipeCmd.Bool("p", false, "Pipe to a public topic")
	replay := pipeCmd.Bool("r", false, "Replay messages to the client that sent it")
	clean := pipeCmd.Bool("c", false, "Don't send status messages")
	dispatcher := pipeCmd.String("d", "multicast", "Type of dispatcher (e.g. multicast, round_robin)")

	if !flagCheck(pipeCmd, topic, cmd.args) {
		return fmt.Errorf("invalid cmd args")
	}

	if pipeCmd.NArg() == 1 && topic == "" {
		topic = pipeCmd.Arg(0)
	}

	handler.Logger.Info(
		"flags parsed",
		"cmd", cmd,
		"public", *public,
		"replay", *replay,
		"topic", topic,
		"access", *access,
		"clean", *clean,
		"dispatcher", *dispatcher,
	)

	var accessList []string

	if *access != "" {
		accessList = parseArgList(*access)
	}

	isCreator := topic == ""
	if isCreator {
		topic = uuid.NewString()
	}

	flagMsg := ""
	if *public {
		flagMsg = "-p "
	}

	// Initial resolution to get the topic name for access storage
	initialResult := resolveTopic(TopicResolveInput{
		UserName: cmd.userName,
		Topic:    topic,
		IsAdmin:  cmd.isAdmin,
		IsPublic: *public,
	})
	name := initialResult.Name

	var accessListCreator bool

	_, loaded := handler.Access.LoadOrStore(name, accessList)
	if !loaded {
		defer func() {
			handler.Access.Delete(name)
		}()
		accessListCreator = true
	}

	// Check for existing access list and resolve final topic name
	existingAccessList, hasExistingAccess := handler.Access.Load(initialResult.WithoutUser)
	result := resolveTopic(TopicResolveInput{
		UserName:           cmd.userName,
		Topic:              topic,
		IsAdmin:            cmd.isAdmin,
		IsPublic:           *public,
		ExistingAccessList: existingAccessList,
		HasExistingAccess:  hasExistingAccess,
		IsAccessCreator:    accessListCreator,
		HasUserAccess:      checkAccess(existingAccessList, cmd.userName, cmd.sesh),
	})
	name = result.Name

	if result.GenerateNewTopic {
		topic = uuid.NewString()
		name = toPublicTopic(topic)
	}

	if isCreator && !*clean {
		fmtTopic := topic
		if *access != "" {
			fmtTopic = fmt.Sprintf("%s/%s", cmd.userName, topic)
		}

		_, _ = fmt.Fprintf(
			cmd.sesh,
			"subscribe to this topic:\n  ssh %s sub %s%s\n",
			toSshCmd(handler.Cfg),
			flagMsg,
			fmtTopic,
		)
	}

	throttledRW := newThrottledMonitorRW(cmd.sesh, handler, cmd, name)

	var dsp psub.MessageDispatcher
	dsp = &psub.MulticastDispatcher{}
	if *dispatcher == "round_robin" {
		dsp = &psub.RoundRobinDispatcher{}
	}
	channel := psub.NewChannel(name)
	_ = handler.PubSub.SetDispatcher(dsp, []*psub.Channel{channel})

	readErr, writeErr := handler.PubSub.Pipe(
		cmd.pipeCtx,
		clientID,
		throttledRW,
		[]*psub.Channel{
			psub.NewChannel(name),
		},
		*replay,
	)

	if readErr != nil && !*clean {
		return readErr
	}

	if writeErr != nil && !*clean {
		return writeErr
	}

	handler.updateMonitor(cmd, name)

	return nil
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

func clientInfo(clients []*psub.Client, isAdmin bool, clientType string) string {
	if len(clients) == 0 {
		return ""
	}

	outputData := fmt.Sprintf("  %s:\r\n", clientType)

	for _, client := range clients {
		if strings.HasPrefix(client.ID, "admin-") && !isAdmin {
			continue
		}

		outputData += fmt.Sprintf("  - %s\r\n", client.ID)
	}

	return outputData
}

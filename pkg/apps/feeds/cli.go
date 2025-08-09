package feeds

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/adhocore/gronx"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/shared"
)

func Middleware(dbpool db.DB, cfg *shared.ConfigSite) pssh.SSHServerMiddleware {
	return func(next pssh.SSHServerHandler) pssh.SSHServerHandler {
		return func(sesh *pssh.SSHServerConnSession) error {
			args := sesh.Command()

			logger := pssh.GetLogger(sesh)
			user := pssh.GetUser(sesh)

			if user == nil {
				err := fmt.Errorf("user not found")
				_, _ = fmt.Fprintln(sesh.Stderr(), err)
				return err
			}

			cmd := "help"
			if len(args) > 0 {
				cmd = args[0]
			}

			switch cmd {
			case "help":
				_, _ = fmt.Fprintf(sesh, "Commands: [help, ls, rm, run]\r\n\r\n")
				writer := tabwriter.NewWriter(sesh, 0, 0, 1, ' ', tabwriter.TabIndent)
				_, _ = fmt.Fprintln(writer, "Cmd\tDesc")
				_, _ = fmt.Fprintf(
					writer,
					"%s\t%s\r\n",
					"help", "this help text",
				)
				_, _ = fmt.Fprintf(
					writer,
					"%s\t%s\r\n",
					"ls", "list feed digest posts with metadata",
				)
				_, _ = fmt.Fprintf(
					writer,
					"%s\t%s\r\n",
					"rm {filename}", "removes feed digest post",
				)
				_, _ = fmt.Fprintf(
					writer,
					"%s\t%s\r\n",
					"run {filename}", "runs the feed digest post immediately, ignoring last digest time validation",
				)
				return writer.Flush()
			case "ls":
				posts, err := dbpool.FindPostsForUser(&db.Pager{Page: 0, Num: 1000}, user.ID, "feeds")
				if err != nil {
					_, _ = fmt.Fprintln(sesh.Stderr(), err)
					return err
				}

				if len(posts.Data) == 0 {
					_, _ = fmt.Fprintln(sesh, "no posts found")
				}

				writer := tabwriter.NewWriter(sesh, 0, 0, 1, ' ', tabwriter.TabIndent)
				_, _ = fmt.Fprintln(writer, "Filename\tLast Digest\tNext Digest\tCron\tFailed Attempts")
				for _, post := range posts.Data {
					parsed := shared.ListParseText(post.Text)

					nextDigest := ""
					cron := parsed.Cron
					if parsed.DigestInterval != "" {
						cron = DigestIntervalToCron(parsed.DigestInterval)
					}
					nd, _ := gronx.NextTickAfter(cron, DateToMin(time.Now()), true)
					nextDigest = nd.Format(time.RFC3339)
					_, _ = fmt.Fprintf(
						writer,
						"%s\t%s\t%s\t%s\t%d/10\r\n",
						post.Filename,
						post.Data.LastDigest.Format(time.RFC3339),
						nextDigest,
						cron,
						post.Data.Attempts,
					)
				}
				return writer.Flush()
			case "rm":
				filename := args[1]
				_, _ = fmt.Fprintf(sesh, "removing digest post %s\r\n", filename)
				write := false
				if len(args) > 2 {
					writeRaw := args[2]
					if writeRaw == "--write" {
						write = true
					}
				}

				post, err := dbpool.FindPostWithFilename(filename, user.ID, "feeds")
				if err != nil {
					_, _ = fmt.Fprintln(sesh.Stderr(), err)
					return err
				}
				if write {
					err = dbpool.RemovePosts([]string{post.ID})
					if err != nil {
						_, _ = fmt.Fprintln(sesh.Stderr(), err)
					}
				}
				_, _ = fmt.Fprintf(sesh, "digest post removed %s\r\n", filename)
				if !write {
					_, _ = fmt.Fprintln(sesh, "WARNING: *must* append with `--write` for the changes to persist.")
				}
				return err
			case "run":
				if len(args) < 2 {
					err := fmt.Errorf("must provide filename of post to run")
					_, _ = fmt.Fprintln(sesh.Stderr(), err)
					return err
				}
				filename := args[1]
				post, err := dbpool.FindPostWithFilename(filename, user.ID, "feeds")
				if err != nil {
					_, _ = fmt.Fprintln(sesh.Stderr(), err)
					return err
				}
				_, _ = fmt.Fprintf(sesh, "running feed post: %s\r\n", filename)
				fetcher := NewFetcher(dbpool, cfg)
				err = fetcher.RunPost(logger, user, post, true, time.Now().UTC())
				if err != nil {
					_, _ = fmt.Fprintln(sesh.Stderr(), err)
				}
				return err
			}

			return next(sesh)
		}
	}
}

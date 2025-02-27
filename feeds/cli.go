package feeds

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"

	wsh "github.com/picosh/pico/wish"
)

func WishMiddleware(dbpool db.DB, cfg *shared.ConfigSite) wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		return func(sesh ssh.Session) {
			args := sesh.Command()
			if len(args) == 0 {
				next(sesh)
				return
			}

			logger := wsh.GetLogger(sesh)
			user := wsh.GetUser(sesh)

			if user == nil {
				wish.Errorln(sesh, fmt.Errorf("user not found"))
				return
			}

			cmd := args[0]
			if cmd == "help" {
				wish.Printf(sesh, "Commands: [help, ls, rm, run]\n\n")
				writer := tabwriter.NewWriter(sesh, 0, 0, 1, ' ', tabwriter.TabIndent)
				fmt.Fprintln(writer, "Cmd\tDesc")
				fmt.Fprintf(
					writer,
					"%s\t%s\n",
					"help", "this help text",
				)
				fmt.Fprintf(
					writer,
					"%s\t%s\n",
					"ls", "list feed digest posts with metadata",
				)
				fmt.Fprintf(
					writer,
					"%s\t%s\n",
					"rm {filename}", "removes feed digest post",
				)
				fmt.Fprintf(
					writer,
					"%s\t%s\n",
					"run {filename}", "runs the feed digest post immediately, ignoring last digest time validation",
				)
				writer.Flush()
				return
			} else if cmd == "ls" {
				posts, err := dbpool.FindPostsForUser(&db.Pager{Page: 0, Num: 1000}, user.ID, "feeds")
				if err != nil {
					wish.Errorln(sesh, err)
					return
				}

				if len(posts.Data) == 0 {
					wish.Println(sesh, "no posts found")
				}

				writer := tabwriter.NewWriter(sesh, 0, 0, 1, ' ', tabwriter.TabIndent)
				fmt.Fprintln(writer, "Filename\tLast Digest\tNext Digest\tInterval\tFailed Attempts")
				for _, post := range posts.Data {
					parsed := shared.ListParseText(post.Text)
					digestOption := DigestOptionToTime(*post.Data.LastDigest, parsed.DigestInterval)
					fmt.Fprintf(
						writer,
						"%s\t%s\t%s\t%s\t%d/10\n",
						post.Filename,
						post.Data.LastDigest.Format(time.RFC3339),
						digestOption.Format(time.RFC3339),
						parsed.DigestInterval,
						post.Data.Attempts,
					)
				}
				writer.Flush()
				return
			} else if cmd == "rm" {
				filename := args[1]
				wish.Printf(sesh, "removing digest post %s\n", filename)
				write := false
				if len(args) > 2 {
					writeRaw := args[2]
					if writeRaw == "--write" {
						write = true
					}
				}

				post, err := dbpool.FindPostWithFilename(filename, user.ID, "feeds")
				if err != nil {
					wish.Errorln(sesh, err)
					return
				}
				if write {
					err = dbpool.RemovePosts([]string{post.ID})
					if err != nil {
						wish.Errorln(sesh, err)
					}
				}
				wish.Printf(sesh, "digest post removed %s\n", filename)
				if !write {
					wish.Println(sesh, "WARNING: *must* append with `--write` for the changes to persist.")
				}
				return
			} else if cmd == "run" {
				if len(args) < 2 {
					wish.Errorln(sesh, "must provide filename of post to run")
					return
				}
				filename := args[1]
				post, err := dbpool.FindPostWithFilename(filename, user.ID, "feeds")
				if err != nil {
					wish.Errorln(sesh, err)
					return
				}
				wish.Printf(sesh, "running feed post: %s\n", filename)
				fetcher := NewFetcher(dbpool, cfg)
				err = fetcher.RunPost(logger, user, post, true)
				if err != nil {
					wish.Errorln(sesh, err)
				}
				return
			}

			next(sesh)
		}
	}
}

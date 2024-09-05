package pubsub

import (
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

var helpStr = "Commands: [pub, sub, ls]\n"

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
					subs := pubsub.PubSub.GetSubs()

					if len(subs) == 0 {
						wish.Println(sesh, "no subs found")
					} else {
						writer := NewTabWriter(sesh)
						fmt.Fprintln(writer, "Channel\tID")
						for _, sub := range subs {
							fmt.Fprintf(
								writer,
								"%s\t%s\n",
								sub.Name, sub.ID,
							)
						}
						writer.Flush()
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
				wish.Println(sesh, "sending msg ...")
				msg := &psub.Msg{
					Name:   fmt.Sprintf("%s@%s", user.Name, repoName),
					Reader: sesh,
				}

				// hacky: we want to notify when no subs are found so
				// we duplicate some logic for now
				subs := pubsub.PubSub.GetSubs()
				found := false
				for _, sub := range subs {
					if pubsub.PubSub.PubMatcher(msg, sub) {
						found = true
						break
					}
				}
				if !found {
					wish.Println(sesh, "no subs found ... waiting")
				}

				err = pubsub.PubSub.Pub(msg)
				wish.Println(sesh, "msg sent!")
				if err != nil {
					wish.Errorln(sesh, err)
				}
			} else if cmd == "sub" {
				err = pubsub.PubSub.Sub(&psub.Subscriber{
					ID:     uuid.NewString(),
					Name:   fmt.Sprintf("%s@%s", user.Name, repoName),
					Writer: sesh,
					Chan:   make(chan error),
				})
				if err != nil {
					wish.Errorln(sesh, err)
				}
			}

			next(sesh)
		}
	}
}

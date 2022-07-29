package internal

import (
	"fmt"
	"io"
	"time"

	"git.sr.ht/~erock/lists.sh/pkg"
	"git.sr.ht/~erock/wish/cms/db"
	"git.sr.ht/~erock/wish/cms/util"
	sendutils "git.sr.ht/~erock/wish/send/utils"
	"github.com/gliderlabs/ssh"
	"golang.org/x/exp/slices"
)

var HiddenPosts = []string{"_readme", "_header"}

type Opener struct {
	entry *sendutils.FileEntry
}

func (o *Opener) Open(name string) (io.Reader, error) {
	return o.entry.Reader, nil
}

type DbHandler struct {
	User   *db.User
	DBPool db.DB
	Cfg    *ConfigSite
}

func NewDbHandler(dbpool db.DB, cfg *ConfigSite) *DbHandler {
	return &DbHandler{
		DBPool: dbpool,
		Cfg:    cfg,
	}
}

func (h *DbHandler) Validate(s ssh.Session) error {
	var err error
	key, err := util.KeyText(s)
	if err != nil {
		return fmt.Errorf("key not found")
	}

	user, err := h.DBPool.FindUserForKey(s.User(), key)
	if err != nil {
		return err
	}

	if user.Name == "" {
		return fmt.Errorf("must have username set")
	}

	h.User = user
	return nil
}

func (h *DbHandler) Write(s ssh.Session, entry *sendutils.FileEntry) (string, error) {
	logger := h.Cfg.Logger
	userID := h.User.ID
	filename := SanitizeFileExt(entry.Name)
	title := filename

	post, err := h.DBPool.FindPostWithFilename(filename, userID, h.Cfg.Space)
	if err != nil {
		logger.Debug("unable to load post, continuing:", err)
	}

	user, err := h.DBPool.FindUser(userID)
	if err != nil {
		return "", fmt.Errorf("error for %s: %v", filename, err)
	}

	var text string
	if b, err := io.ReadAll(entry.Reader); err == nil {
		text = string(b)
	}

	if !IsTextFile(text, entry.Filepath) {
		return "", fmt.Errorf("WARNING: (%s) invalid file, format must be '.txt' and the contents must be plain text, skipping", entry.Name)
	}

	parsedText := pkg.ParseText(text)
	if parsedText.MetaData.Title != "" {
		title = parsedText.MetaData.Title
	}
	description := parsedText.MetaData.Description

	// if the file is empty we remove it from our database
	if len(text) == 0 {
		// skip empty files from being added to db
		if post == nil {
			logger.Infof("(%s) is empty, skipping record", filename)
			return "", nil
		}

		err := h.DBPool.RemovePosts([]string{post.ID})
		logger.Infof("(%s) is empty, removing record", filename)
		if err != nil {
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}
	} else if post == nil {
		publishAt := time.Now()
		if parsedText.MetaData.PublishAt != nil {
			publishAt = *parsedText.MetaData.PublishAt
		}
		hidden := slices.Contains(HiddenPosts, filename)

		logger.Infof("(%s) not found, adding record", filename)
		_, err = h.DBPool.InsertPost(userID, filename, title, text, description, &publishAt, hidden, h.Cfg.Space)
		if err != nil {
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}
	} else {
		publishAt := post.PublishAt
		if parsedText.MetaData.PublishAt != nil {
			publishAt = parsedText.MetaData.PublishAt
		}
		if text == post.Text {
			logger.Infof("(%s) found, but text is identical, skipping", filename)
			return h.Cfg.PostURL(user.Name, filename), nil
		}

		logger.Infof("(%s) found, updating record", filename)
		_, err = h.DBPool.UpdatePost(post.ID, title, text, description, publishAt)
		if err != nil {
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}
	}

	return h.Cfg.PostURL(user.Name, filename), nil
}

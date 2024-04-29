package pico

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/filehandlers/util"
	"github.com/picosh/pico/shared"
	"github.com/picosh/send/send/utils"
)

type UploadHandler struct {
	DBPool db.DB
	Cfg    *shared.ConfigSite
}

func NewUploadHandler(dbpool db.DB, cfg *shared.ConfigSite) *UploadHandler {
	return &UploadHandler{
		DBPool: dbpool,
		Cfg:    cfg,
	}
}

func (h *UploadHandler) getAuthorizedKeyFile(user *db.User) (*utils.VirtualFile, string, error) {
	keys, err := h.DBPool.FindKeysForUser(user)
	text := ""
	var modTime time.Time
	for _, pk := range keys {
		text += fmt.Sprintf("%s %s\n", pk.Key, pk.Name)
		modTime = *pk.CreatedAt
	}
	if err != nil {
		return nil, "", err
	}
	fileInfo := &utils.VirtualFile{
		FName:    "authorized_keys",
		FIsDir:   false,
		FSize:    int64(len(text)),
		FModTime: modTime,
	}
	return fileInfo, text, nil
}

func (h *UploadHandler) Read(s ssh.Session, entry *utils.FileEntry) (os.FileInfo, utils.ReaderAtCloser, error) {
	user, err := util.GetUser(s)
	if err != nil {
		return nil, nil, err
	}
	cleanFilename := filepath.Base(entry.Filepath)

	if cleanFilename == "" || cleanFilename == "." {
		return nil, nil, os.ErrNotExist
	}

	if cleanFilename == "authorized_keys" {
		fileInfo, text, err := h.getAuthorizedKeyFile(user)
		if err != nil {
			return nil, nil, err
		}
		reader := utils.NopReaderAtCloser(strings.NewReader(text))
		return fileInfo, reader, nil
	}

	return nil, nil, os.ErrNotExist
}

func (h *UploadHandler) List(s ssh.Session, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error) {
	var fileList []os.FileInfo
	user, err := util.GetUser(s)
	if err != nil {
		return fileList, err
	}
	cleanFilename := filepath.Base(fpath)

	if cleanFilename == "" || cleanFilename == "." || cleanFilename == "/" {
		name := cleanFilename
		if name == "" {
			name = "/"
		}

		fileList = append(fileList, &utils.VirtualFile{
			FName:  name,
			FIsDir: true,
		})

		flist, _, err := h.getAuthorizedKeyFile(user)
		if err != nil {
			return fileList, err
		}
		fileList = append(fileList, flist)
	} else {
		if cleanFilename == "authorized_keys" {
			flist, _, err := h.getAuthorizedKeyFile(user)
			if err != nil {
				return fileList, err
			}
			fileList = append(fileList, flist)
		}
	}

	return fileList, nil
}

func (h *UploadHandler) GetLogger() *slog.Logger {
	return h.Cfg.Logger
}

func (h *UploadHandler) Validate(s ssh.Session) error {
	var err error
	key, err := utils.KeyText(s)
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

	util.SetUser(s, user)
	return nil
}

type KeyWithId struct {
	Pk      ssh.PublicKey
	ID      string
	Comment string
}

type KeyDiffResult struct {
	Add []KeyWithId
	Rm  []string
}

func authorizedKeysDiff(keyInUse ssh.PublicKey, curKeys []KeyWithId, nextKeys []KeyWithId) KeyDiffResult {
	add := []KeyWithId{}
	for _, nk := range nextKeys {
		found := false
		for _, ck := range curKeys {
			if ssh.KeysEqual(nk.Pk, ck.Pk) {
				found = true
				break
			}
		}
		if !found {
			add = append(add, nk)
		}
	}

	rm := []string{}
	for _, ck := range curKeys {
		// we never want to remove the key that's in the current ssh session
		// in an effort to avoid mistakenly removing their current key
		if ssh.KeysEqual(ck.Pk, keyInUse) {
			continue
		}

		found := false
		for _, nk := range nextKeys {
			if ssh.KeysEqual(ck.Pk, nk.Pk) {
				found = true
				break
			}
		}
		if !found {
			rm = append(rm, ck.ID)
		}
	}

	return KeyDiffResult{
		Add: add,
		Rm:  rm,
	}
}

func (h *UploadHandler) ProcessAuthorizedKeys(text []byte, logger *slog.Logger, user *db.User, s ssh.Session) error {
	dbpool := h.DBPool

	curKeysStr, err := dbpool.FindKeysForUser(user)
	if err != nil {
		return err
	}

	splitKeys := bytes.Split(text, []byte{'\n'})
	nextKeys := []KeyWithId{}
	for _, pk := range splitKeys {
		key, comment, _, _, err := ssh.ParseAuthorizedKey(bytes.TrimSpace(pk))
		if err != nil {
			continue
		}
		nextKeys = append(nextKeys, KeyWithId{Pk: key, Comment: comment})
	}

	curKeys := []KeyWithId{}
	for _, pk := range curKeysStr {
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pk.Key))
		if err != nil {
			continue
		}
		curKeys = append(curKeys, KeyWithId{Pk: key, ID: pk.ID})
	}

	diff := authorizedKeysDiff(s.PublicKey(), curKeys, nextKeys)

	for _, pk := range diff.Add {
		key, err := shared.KeyForKeyText(pk.Pk)
		if err != nil {
			continue
		}

		logger.Info("adding pubkey for user", "pubkey", key)

		_, err = dbpool.InsertPublicKey(user.ID, key, pk.Comment, nil)
		if err != nil {
			logger.Error("could not insert pubkey", "err", err.Error())
		}
	}

	if len(diff.Rm) > 0 {
		logger.Info("removing pubkeys for user", "pubkeys", diff.Rm)

		err = dbpool.RemoveKeys(diff.Rm)
		if err != nil {
			logger.Error("could not remove pubkey", "err", err.Error())
		}
	}

	return nil
}

func (h *UploadHandler) Write(s ssh.Session, entry *utils.FileEntry) (string, error) {
	logger := h.Cfg.Logger
	user, err := util.GetUser(s)
	if err != nil {
		logger.Error(err.Error())
		return "", err
	}

	filename := filepath.Base(entry.Filepath)
	logger = logger.With(
		"user", user.Name,
		"filename", filename,
		"space", h.Cfg.Space,
	)

	var text []byte
	if b, err := io.ReadAll(entry.Reader); err == nil {
		text = b
	}

	if filename == "authorized_keys" {
		err := h.ProcessAuthorizedKeys(text, logger, user, s)
		if err != nil {
			return "", err
		}
	} else {
		return "", fmt.Errorf("validation error: invalid file, received %s", entry.Filepath)
	}

	return "", nil
}

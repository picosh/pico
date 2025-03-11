package pico

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/pssh"
	"github.com/picosh/pico/shared"
	sendutils "github.com/picosh/send/utils"
	"github.com/picosh/utils"
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

func (h *UploadHandler) getAuthorizedKeyFile(user *db.User) (*sendutils.VirtualFile, string, error) {
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
	fileInfo := &sendutils.VirtualFile{
		FName:    "authorized_keys",
		FIsDir:   false,
		FSize:    int64(len(text)),
		FModTime: modTime,
	}
	return fileInfo, text, nil
}

func (h *UploadHandler) Delete(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) error {
	return errors.New("unsupported")
}

func (h *UploadHandler) Read(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) (os.FileInfo, sendutils.ReadAndReaderAtCloser, error) {
	logger := pssh.GetLogger(s)
	user := pssh.GetUser(s)

	if user == nil {
		err := fmt.Errorf("could not get user from ctx")
		logger.Error("error getting user from ctx", "err", err)
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
		reader := sendutils.NopReadAndReaderAtCloser(strings.NewReader(text))
		return fileInfo, reader, nil
	}

	return nil, nil, os.ErrNotExist
}

func (h *UploadHandler) List(s *pssh.SSHServerConnSession, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error) {
	var fileList []os.FileInfo

	logger := pssh.GetLogger(s)
	user := pssh.GetUser(s)

	if user == nil {
		err := fmt.Errorf("could not get user from ctx")
		logger.Error("error getting user from ctx", "err", err)
		return fileList, err
	}

	cleanFilename := filepath.Base(fpath)

	if cleanFilename == "" || cleanFilename == "." || cleanFilename == "/" {
		name := cleanFilename
		if name == "" {
			name = "/"
		}

		fileList = append(fileList, &sendutils.VirtualFile{
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

func (h *UploadHandler) GetLogger(s *pssh.SSHServerConnSession) *slog.Logger {
	return pssh.GetLogger(s)
}

func (h *UploadHandler) Validate(s *pssh.SSHServerConnSession) error {
	var err error
	key, err := sendutils.KeyText(s)
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

	s.Permissions().Extensions["user_id"] = user.ID
	return nil
}

type KeyWithId struct {
	Pk      ssh.PublicKey
	ID      string
	Comment string
}

type KeyDiffResult struct {
	Add    []KeyWithId
	Rm     []string
	Update []KeyWithId
}

func authorizedKeysDiff(keyInUse ssh.PublicKey, curKeys []KeyWithId, nextKeys []KeyWithId) KeyDiffResult {
	update := []KeyWithId{}
	add := []KeyWithId{}
	for _, nk := range nextKeys {
		found := false
		for _, ck := range curKeys {
			if ssh.KeysEqual(nk.Pk, ck.Pk) {
				found = true

				// update the comment field
				if nk.Comment != ck.Comment {
					ck.Comment = nk.Comment
					update = append(update, ck)
				}
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
		Add:    add,
		Rm:     rm,
		Update: update,
	}
}

func (h *UploadHandler) ProcessAuthorizedKeys(text []byte, logger *slog.Logger, user *db.User, s *pssh.SSHServerConnSession) error {
	logger.Info("processing new authorized_keys")
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
		curKeys = append(curKeys, KeyWithId{Pk: key, ID: pk.ID, Comment: pk.Name})
	}

	diff := authorizedKeysDiff(s.PublicKey(), curKeys, nextKeys)

	for _, pk := range diff.Add {
		key := utils.KeyForKeyText(pk.Pk)

		fmt.Fprintf(s.Stderr(), "adding pubkey (%s)\n", key)
		logger.Info("adding pubkey", "pubkey", key)

		err = dbpool.InsertPublicKey(user.ID, key, pk.Comment, nil)
		if err != nil {
			fmt.Fprintf(s.Stderr(), "error: could not insert pubkey: %s (%s)\n", err.Error(), key)
			logger.Error("could not insert pubkey", "err", err.Error())
		}
	}

	for _, pk := range diff.Update {
		key := utils.KeyForKeyText(pk.Pk)

		fmt.Fprintf(s.Stderr(), "updating pubkey with comment: %s (%s)\n", pk.Comment, key)
		logger.Info(
			"updating pubkey with comment",
			"pubkey", key,
			"comment", pk.Comment,
		)

		_, err = dbpool.UpdatePublicKey(pk.ID, pk.Comment)
		if err != nil {
			fmt.Fprintf(s.Stderr(), "error: could not update pubkey: %s (%s)\n", err.Error(), key)
			logger.Error("could not update pubkey", "err", err.Error(), "key", key)
		}
	}

	if len(diff.Rm) > 0 {
		fmt.Fprintf(s.Stderr(), "removing pubkeys: %s\n", diff.Rm)
		logger.Info("removing pubkeys", "pubkeys", diff.Rm)

		err = dbpool.RemoveKeys(diff.Rm)
		if err != nil {
			fmt.Fprintf(s.Stderr(), "error: could not rm pubkeys: %s\n", err.Error())
			logger.Error("could not remove pubkey", "err", err.Error())
		}
	}

	return nil
}

func (h *UploadHandler) Write(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) (string, error) {
	logger := pssh.GetLogger(s)
	user := pssh.GetUser(s)

	if user == nil {
		err := fmt.Errorf("could not get user from ctx")
		logger.Error("error getting user from ctx", "err", err)
		return "", err
	}

	filename := filepath.Base(entry.Filepath)
	logger = logger.With(
		"user", user.Name,
		"filename", filename,
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

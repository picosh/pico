package uploadassets

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/pico/wish/cms/util"
	"github.com/picosh/send/send/utils"
	"go.uber.org/zap"
)

type ctxUserKey struct{}
type ctxFeatureFlagKey struct{}
type ctxBucketKey struct{}
type ctxBucketQuotaKey struct{}
type ctxProjectKey struct{}

func getProject(s ssh.Session) *db.Project {
	v := s.Context().Value(ctxProjectKey{})
	if v == nil {
		return nil
	}
	project := s.Context().Value(ctxProjectKey{}).(*db.Project)
	return project
}

func getBucket(s ssh.Session) (storage.Bucket, error) {
	bucket := s.Context().Value(ctxBucketKey{}).(storage.Bucket)
	if bucket.Name == "" {
		return bucket, fmt.Errorf("bucket not set on `ssh.Context()` for connection")
	}
	return bucket, nil
}

func getFeatureFlag(s ssh.Session) (*db.FeatureFlag, error) {
	ff := s.Context().Value(ctxFeatureFlagKey{}).(*db.FeatureFlag)
	if ff.Name == "" {
		return ff, fmt.Errorf("feature flag not set on `ssh.Context()` for connection")
	}
	return ff, nil
}

func getBucketQuota(s ssh.Session) uint64 {
	return s.Context().Value(ctxBucketQuotaKey{}).(uint64)
}

func getUser(s ssh.Session) (*db.User, error) {
	user := s.Context().Value(ctxUserKey{}).(*db.User)
	if user == nil {
		return user, fmt.Errorf("user not set on `ssh.Context()` for connection")
	}
	return user, nil
}

type FileData struct {
	*utils.FileEntry
	Text        []byte
	User        *db.User
	Bucket      storage.Bucket
	BucketQuota uint64
	FeatureFlag *db.FeatureFlag
}

type UploadAssetHandler struct {
	DBPool  db.DB
	Cfg     *shared.ConfigSite
	Storage storage.ObjectStorage
}

func NewUploadAssetHandler(dbpool db.DB, cfg *shared.ConfigSite, storage storage.ObjectStorage) *UploadAssetHandler {
	return &UploadAssetHandler{
		DBPool:  dbpool,
		Cfg:     cfg,
		Storage: storage,
	}
}

func (h *UploadAssetHandler) GetLogger() *zap.SugaredLogger {
	return h.Cfg.Logger
}

func (h *UploadAssetHandler) Read(s ssh.Session, entry *utils.FileEntry) (os.FileInfo, utils.ReaderAtCloser, error) {
	user, err := getUser(s)
	if err != nil {
		return nil, nil, err
	}

	fileInfo := &utils.VirtualFile{
		FName:    filepath.Base(entry.Filepath),
		FIsDir:   false,
		FSize:    entry.Size,
		FModTime: time.Unix(entry.Mtime, 0),
	}

	bucket, err := h.Storage.GetBucket(shared.GetAssetBucketName(user.ID))
	if err != nil {
		return nil, nil, err
	}

	fname := shared.GetAssetFileName(entry)
	contents, size, modTime, err := h.Storage.GetFile(bucket, fname)
	if err != nil {
		return nil, nil, err
	}

	fileInfo.FSize = size
	fileInfo.FModTime = modTime

	reader := shared.NewAllReaderAt(contents)

	return fileInfo, reader, nil
}

func (h *UploadAssetHandler) List(s ssh.Session, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error) {
	var fileList []os.FileInfo

	user, err := getUser(s)
	if err != nil {
		return fileList, err
	}

	cleanFilename := fpath

	bucketName := shared.GetAssetBucketName(user.ID)
	bucket, err := h.Storage.GetBucket(bucketName)
	if err != nil {
		return fileList, err
	}

	if cleanFilename == "" || cleanFilename == "." {
		name := cleanFilename
		if name == "" {
			name = "/"
		}

		info := &utils.VirtualFile{
			FName:  name,
			FIsDir: true,
		}

		fileList = append(fileList, info)
	} else {
		if cleanFilename != "/" && isDir {
			cleanFilename += "/"
		}

		foundList, err := h.Storage.ListFiles(bucket, cleanFilename, recursive)
		if err != nil {
			return fileList, err
		}

		fileList = append(fileList, foundList...)
	}

	return fileList, nil
}

func (h *UploadAssetHandler) Validate(s ssh.Session) error {
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

	ff, err := h.DBPool.FindFeatureForUser(user.ID, "pgs")
	// pgs.sh has a free tier so users might not have a feature flag
	// in which case we set sane defaults
	if err != nil {
		ff = db.NewFeatureFlag(
			user.ID,
			"pgs",
			h.Cfg.MaxSize,
			h.Cfg.MaxAssetSize,
		)
	}
	s.Context().SetValue(ctxFeatureFlagKey{}, ff)

	assetBucket := shared.GetAssetBucketName(user.ID)
	bucket, err := h.Storage.UpsertBucket(assetBucket)
	if err != nil {
		return err
	}
	s.Context().SetValue(ctxBucketKey{}, bucket)

	totalFileSize, err := h.Storage.GetBucketQuota(bucket)
	if err != nil {
		return err
	}
	s.Context().SetValue(ctxBucketQuotaKey{}, totalFileSize)
	h.Cfg.Logger.Infof("(%s) bucket size is current (%d bytes)", user.Name, totalFileSize)

	s.Context().SetValue(ctxUserKey{}, user)
	h.Cfg.Logger.Infof("(%s) attempting to upload files to (%s)", user.Name, h.Cfg.Space)

	return nil
}

func (h *UploadAssetHandler) Write(s ssh.Session, entry *utils.FileEntry) (string, error) {
	user, err := getUser(s)
	if err != nil {
		h.Cfg.Logger.Error(err)
		return "", err
	}

	var origText []byte
	if b, err := io.ReadAll(entry.Reader); err == nil {
		origText = b
	}
	fileSize := binary.Size(origText)
	// TODO: hack for now until I figure out how to get correct
	// filesize from sftp,scp,rsync
	entry.Size = int64(fileSize)

	bucket, err := getBucket(s)
	if err != nil {
		h.Cfg.Logger.Error(err)
		return "", err
	}

	hasProject := getProject(s)
	projectName := shared.GetProjectName(entry)

	// find, create, or update project if we haven't already done it
	if hasProject == nil {
		project, err := h.DBPool.FindProjectByName(user.ID, projectName)
		if err == nil {
			err = h.DBPool.UpdateProject(user.ID, projectName)
			if err != nil {
				h.Cfg.Logger.Error(err)
				return "", err
			}
		} else {
			_, err = h.DBPool.InsertProject(user.ID, projectName, projectName)
			if err != nil {
				h.Cfg.Logger.Error(err)
				return "", err
			}
			project, err = h.DBPool.FindProjectByName(user.ID, projectName)
			if err != nil {
				h.Cfg.Logger.Error(err)
				return "", err
			}
		}
		s.Context().SetValue(ctxProjectKey{}, project)
	}

	bucketQuota := getBucketQuota(s)
	featureFlag, err := getFeatureFlag(s)
	if err != nil {
		return "", err
	}
	data := &FileData{
		FileEntry:   entry,
		User:        user,
		Text:        origText,
		Bucket:      bucket,
		BucketQuota: bucketQuota,
		FeatureFlag: featureFlag,
	}
	err = h.writeAsset(data)
	if err != nil {
		h.Cfg.Logger.Error(err)
		return "", err
	}

	url := h.Cfg.AssetURL(
		user.Name,
		projectName,
		strings.Replace(data.Filepath, "/"+projectName+"/", "", 1),
	)

	totalFileSize := bucketQuota
	maxSize := int(featureFlag.Data.StorageMax)
	str := fmt.Sprintf(
		"%s (space: %.2f/%.2fGB, %.2f%%)",
		url,
		shared.BytesToGB(int(totalFileSize)),
		shared.BytesToGB(maxSize),
		(float32(totalFileSize)/float32(maxSize))*100,
	)

	return str, nil
}

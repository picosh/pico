package uploadimgs

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/ssh"
	exifremove "github.com/neurosnap/go-exif-remove"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/pico/wish"
	"github.com/picosh/pobj"
	sst "github.com/picosh/pobj/storage"
	sendutils "github.com/picosh/send/utils"
	"github.com/picosh/utils"
)

var Space = "imgs"

type PostMetaData struct {
	Text          []byte
	FileSize      int
	TotalFileSize int
	Filename      string
	User          *db.User
	FeatureFlag   *db.FeatureFlag
	Bucket        sst.Bucket
}

type UploadImgHandler struct {
	DBPool  db.DB
	Cfg     *shared.ConfigSite
	Storage storage.StorageServe
}

func NewUploadImgHandler(dbpool db.DB, cfg *shared.ConfigSite, storage storage.StorageServe) *UploadImgHandler {
	return &UploadImgHandler{
		DBPool:  dbpool,
		Cfg:     cfg,
		Storage: storage,
	}
}

func (h *UploadImgHandler) getObjectPath(fpath string) string {
	return filepath.Join("prose", fpath)
}

func (h *UploadImgHandler) List(s ssh.Session, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error) {
	var fileList []os.FileInfo

	logger := wish.GetLogger(s)
	user := wish.GetUser(s)

	if user == nil {
		err := fmt.Errorf("could not get user from ctx")
		logger.Error("error getting user from ctx", "err", err)
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

		info := &sendutils.VirtualFile{
			FName:  name,
			FIsDir: true,
		}

		fileList = append(fileList, info)
	} else {
		fp := h.getObjectPath(cleanFilename)
		if fp != "/" && isDir {
			fp += "/"
		}

		foundList, err := h.Storage.ListObjects(bucket, fp, recursive)
		if err != nil {
			return fileList, err
		}

		fileList = append(fileList, foundList...)
	}

	return fileList, nil
}

func (h *UploadImgHandler) Read(s ssh.Session, entry *sendutils.FileEntry) (os.FileInfo, sendutils.ReadAndReaderAtCloser, error) {
	logger := wish.GetLogger(s)
	user := wish.GetUser(s)

	if user == nil {
		err := fmt.Errorf("could not get user from ctx")
		logger.Error("error getting user from ctx", "err", err)
		return nil, nil, err
	}

	cleanFilename := filepath.Base(entry.Filepath)

	if cleanFilename == "" || cleanFilename == "." {
		return nil, nil, os.ErrNotExist
	}

	bucket, err := h.Storage.GetBucket(shared.GetAssetBucketName(user.ID))
	if err != nil {
		return nil, nil, err
	}

	contents, info, err := h.Storage.GetObject(bucket, h.getObjectPath(cleanFilename))
	if err != nil {
		return nil, nil, err
	}
	reader := pobj.NewAllReaderAt(contents)

	fileInfo := &sendutils.VirtualFile{
		FName:    cleanFilename,
		FIsDir:   false,
		FSize:    info.Size,
		FModTime: info.LastModified,
	}

	return fileInfo, reader, nil
}

func (h *UploadImgHandler) Write(s ssh.Session, entry *sendutils.FileEntry) (string, error) {
	logger := wish.GetLogger(s)
	user := wish.GetUser(s)

	if user == nil {
		err := fmt.Errorf("could not get user from ctx")
		logger.Error("error getting user from ctx", "err", err)
		return "", err
	}

	filename := filepath.Base(entry.Filepath)

	var text []byte
	if b, err := io.ReadAll(entry.Reader); err == nil {
		text = b
	}
	mimeType := http.DetectContentType(text)
	ext := filepath.Ext(filename)
	if ext == ".svg" {
		mimeType = "image/svg+xml"
	}
	// strip exif data
	if slices.Contains([]string{"image/png", "image/jpg", "image/jpeg"}, mimeType) {
		noExifBytes, err := exifremove.Remove(text)
		if err == nil {
			if len(noExifBytes) == 0 {
				logger.Info("file silently failed to strip exif data", "filename", filename)
			} else {
				text = noExifBytes
				logger.Info("stripped exif data", "filename", filename)
			}
		} else {
			logger.Error("could not strip exif data", "err", err.Error())
		}
	}

	fileSize := binary.Size(text)
	featureFlag := shared.FindPlusFF(h.DBPool, h.Cfg, user.ID)

	bucket, err := h.Storage.UpsertBucket(shared.GetAssetBucketName(user.ID))
	if err != nil {
		return "", err
	}

	totalFileSize, err := h.Storage.GetBucketQuota(bucket)
	if err != nil {
		logger.Error("bucket quota", "err", err)
		return "", err
	}

	metadata := PostMetaData{
		Filename:      filename,
		FileSize:      fileSize,
		Text:          text,
		User:          user,
		FeatureFlag:   featureFlag,
		Bucket:        bucket,
		TotalFileSize: int(totalFileSize),
	}

	err = h.writeImg(s, &metadata)
	if err != nil {
		logger.Error("could not write img", "err", err.Error())
		return "", err
	}

	curl := shared.NewCreateURL(h.Cfg)
	url := h.Cfg.FullPostURL(
		curl,
		user.Name,
		metadata.Filename,
	)
	maxSize := int(featureFlag.Data.StorageMax)
	str := fmt.Sprintf(
		"%s (space: %.2f/%.2fGB, %.2f%%)",
		url,
		utils.BytesToGB(metadata.TotalFileSize+fileSize),
		utils.BytesToGB(maxSize),
		(float32(totalFileSize)/float32(maxSize))*100,
	)
	return str, nil
}

func (h *UploadImgHandler) Delete(s ssh.Session, entry *sendutils.FileEntry) error {
	logger := wish.GetLogger(s)
	user := wish.GetUser(s)

	if user == nil {
		err := fmt.Errorf("could not get user from ctx")
		logger.Error("error getting user from ctx", "err", err)
		return err
	}

	filename := filepath.Base(entry.Filepath)

	logger = logger.With(
		"filename", filename,
	)

	bucket, err := h.Storage.UpsertBucket(shared.GetAssetBucketName(user.ID))
	if err != nil {
		return err
	}

	logger.Info("deleting image")
	err = h.Storage.DeleteObject(bucket, h.getObjectPath(filename))
	if err != nil {
		return err
	}

	return nil
}

func (h *UploadImgHandler) validateImg(data *PostMetaData) (bool, error) {
	fileMax := data.FeatureFlag.Data.FileMax
	if int64(data.FileSize) > fileMax {
		return false, fmt.Errorf("ERROR: file (%s) has exceeded maximum file size (%d bytes)", data.Filename, fileMax)
	}

	storageMax := data.FeatureFlag.Data.StorageMax
	if uint64(data.TotalFileSize+data.FileSize) > storageMax {
		return false, fmt.Errorf("ERROR: user (%s) has exceeded (%d bytes) max (%d bytes)", data.User.Name, data.TotalFileSize, storageMax)
	}

	if !utils.IsExtAllowed(data.Filename, h.Cfg.AllowedExt) {
		extStr := strings.Join(h.Cfg.AllowedExt, ",")
		err := fmt.Errorf(
			"ERROR: (%s) invalid file, format must be (%s), skipping",
			data.Filename,
			extStr,
		)
		return false, err
	}

	return true, nil
}

func (h *UploadImgHandler) metaImg(data *PostMetaData) error {
	// if the file is empty that means we should delete it
	// so we can skip all the meta info
	if data.FileSize == 0 {
		return nil
	}

	// make sure we have a bucket
	bucket, err := h.Storage.UpsertBucket(shared.GetAssetBucketName(data.User.ID))
	if err != nil {
		return err
	}

	// make sure we have a prose project to upload to
	_, err = h.DBPool.UpsertProject(data.User.ID, "prose", "prose")
	if err != nil {
		return err
	}

	reader := bytes.NewReader([]byte(data.Text))
	_, _, err = h.Storage.PutObject(
		bucket,
		h.getObjectPath(data.Filename),
		sendutils.NopReadAndReaderAtCloser(reader),
		&sendutils.FileEntry{},
	)
	if err != nil {
		return err
	}

	return nil
}

func (h *UploadImgHandler) writeImg(s ssh.Session, data *PostMetaData) error {
	valid, err := h.validateImg(data)
	if !valid {
		return err
	}

	logger := wish.GetLogger(s)
	logger = logger.With(
		"filename", data.Filename,
	)

	logger.Info("uploading image")
	err = h.metaImg(data)
	if err != nil {
		logger.Error("meta img", "err", err)
		return err
	}

	return nil
}

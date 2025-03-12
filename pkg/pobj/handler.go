package pobj

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/picosh/pico/pkg/pobj/storage"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/utils"
)

type ctxBucketKey struct{}

func getBucket(ctx *pssh.SSHServerConnSession) (storage.Bucket, error) {
	bucket, ok := ctx.Value(ctxBucketKey{}).(storage.Bucket)
	if !ok {
		return bucket, fmt.Errorf("bucket not set on `ssh.Context()` for connection")
	}
	if bucket.Name == "" {
		return bucket, fmt.Errorf("bucket not set on `ssh.Context()` for connection")
	}
	return bucket, nil
}
func setBucket(ctx *pssh.SSHServerConnSession, bucket storage.Bucket) {
	ctx.SetValue(ctxBucketKey{}, bucket)
}

type FileData struct {
	*utils.FileEntry
	Text   []byte
	User   string
	Bucket storage.Bucket
}

type Config struct {
	Logger     *slog.Logger
	Storage    storage.ObjectStorage
	AssetNames AssetNames
}

type UploadAssetHandler struct {
	Cfg *Config
}

var _ utils.CopyFromClientHandler = &UploadAssetHandler{}
var _ utils.CopyFromClientHandler = (*UploadAssetHandler)(nil)

func NewUploadAssetHandler(cfg *Config) *UploadAssetHandler {
	if cfg.AssetNames == nil {
		cfg.AssetNames = &AssetNamesBasic{}
	}

	return &UploadAssetHandler{
		Cfg: cfg,
	}
}

func (h *UploadAssetHandler) GetLogger(s *pssh.SSHServerConnSession) *slog.Logger {
	return h.Cfg.Logger
}

func (h *UploadAssetHandler) Delete(s *pssh.SSHServerConnSession, entry *utils.FileEntry) error {
	h.Cfg.Logger.Info("deleting file", "file", entry.Filepath)
	bucket, err := getBucket(s)
	if err != nil {
		h.Cfg.Logger.Error(err.Error())
		return err
	}

	objectFileName, err := h.Cfg.AssetNames.ObjectName(s, entry)
	if err != nil {
		return err
	}
	return h.Cfg.Storage.DeleteObject(bucket, objectFileName)
}

func (h *UploadAssetHandler) Read(s *pssh.SSHServerConnSession, entry *utils.FileEntry) (os.FileInfo, utils.ReadAndReaderAtCloser, error) {
	fileInfo := &utils.VirtualFile{
		FName:    filepath.Base(entry.Filepath),
		FIsDir:   false,
		FSize:    entry.Size,
		FModTime: time.Unix(entry.Mtime, 0),
	}
	h.Cfg.Logger.Info("reading file", "file", fileInfo)

	bucketName, err := h.Cfg.AssetNames.BucketName(s)
	if err != nil {
		return nil, nil, err
	}
	bucket, err := h.Cfg.Storage.GetBucket(bucketName)
	if err != nil {
		return nil, nil, err
	}

	fname, err := h.Cfg.AssetNames.ObjectName(s, entry)
	if err != nil {
		return nil, nil, err
	}
	contents, info, err := h.Cfg.Storage.GetObject(bucket, fname)
	if err != nil {
		return nil, nil, err
	}

	fileInfo.FSize = info.Size
	fileInfo.FModTime = info.LastModified

	reader := NewAllReaderAt(contents)

	return fileInfo, reader, nil
}

func (h *UploadAssetHandler) List(s *pssh.SSHServerConnSession, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error) {
	h.Cfg.Logger.Info(
		"listing path",
		"dir", fpath,
		"isDir", isDir,
		"recursive", recursive,
	)
	var fileList []os.FileInfo

	cleanFilename := fpath

	bucketName, err := h.Cfg.AssetNames.BucketName(s)
	if err != nil {
		return fileList, err
	}
	bucket, err := h.Cfg.Storage.GetBucket(bucketName)
	if err != nil {
		return fileList, err
	}

	fname, err := h.Cfg.AssetNames.ObjectName(s, &utils.FileEntry{Filepath: cleanFilename})
	if err != nil {
		return fileList, err
	}

	if fname == "" || fname == "." {
		name := fname
		if name == "" {
			name = "/"
		}

		info := &utils.VirtualFile{
			FName:  name,
			FIsDir: true,
		}

		fileList = append(fileList, info)
	} else {
		name := fname
		if name != "/" && isDir {
			name += "/"
		}

		foundList, err := h.Cfg.Storage.ListObjects(bucket, name, recursive)
		if err != nil {
			return fileList, err
		}

		fileList = append(fileList, foundList...)
	}

	return fileList, nil
}

func (h *UploadAssetHandler) Validate(s *pssh.SSHServerConnSession) error {
	var err error
	userName := s.User()

	assetBucket, err := h.Cfg.AssetNames.BucketName(s)
	if err != nil {
		return err
	}
	bucket, err := h.Cfg.Storage.UpsertBucket(assetBucket)
	if err != nil {
		return err
	}
	setBucket(s, bucket)

	pk, _ := utils.KeyText(s)
	h.Cfg.Logger.Info(
		"attempting to upload files",
		"user", userName,
		"bucket", bucket.Name,
		"publicKey", pk,
	)
	return nil
}

func (h *UploadAssetHandler) Write(s *pssh.SSHServerConnSession, entry *utils.FileEntry) (string, error) {
	var origText []byte
	if b, err := io.ReadAll(entry.Reader); err == nil {
		origText = b
	}
	fileSize := binary.Size(origText)
	// TODO: hack for now until I figure out how to get correct
	// filesize from sftp,scp,rsync
	entry.Size = int64(fileSize)
	userName := s.User()

	bucket, err := getBucket(s)
	if err != nil {
		h.Cfg.Logger.Error(err.Error())
		return "", err
	}

	data := &FileData{
		FileEntry: entry,
		User:      userName,
		Text:      origText,
		Bucket:    bucket,
	}
	err = h.writeAsset(s, data)
	if err != nil {
		h.Cfg.Logger.Error(err.Error())
		return "", err
	}

	url, err := h.Cfg.AssetNames.PrintObjectName(s, entry, bucket.Name)
	if err != nil {
		return "", err
	}
	return url, nil
}

func (h *UploadAssetHandler) validateAsset(_ *FileData) (bool, error) {
	return true, nil
}

func (h *UploadAssetHandler) writeAsset(s *pssh.SSHServerConnSession, data *FileData) error {
	valid, err := h.validateAsset(data)
	if !valid {
		return err
	}

	objectFileName, err := h.Cfg.AssetNames.ObjectName(s, data.FileEntry)
	if err != nil {
		return err
	}
	reader := bytes.NewReader(data.Text)

	h.Cfg.Logger.Info(
		"uploading file to bucket",
		"user",
		data.User,
		"bucket",
		data.Bucket.Name,
		"object",
		objectFileName,
	)

	_, _, err = h.Cfg.Storage.PutObject(
		data.Bucket,
		objectFileName,
		utils.NopReadAndReaderAtCloser(reader),
		data.FileEntry,
	)
	if err != nil {
		return err
	}

	return nil
}

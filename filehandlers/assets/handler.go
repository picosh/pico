package uploadassets

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	futil "github.com/picosh/pico/filehandlers/util"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/pobj"
	sst "github.com/picosh/pobj/storage"
	"github.com/picosh/send/send/utils"
	ignore "github.com/sabhiram/go-gitignore"
)

type ctxBucketKey struct{}
type ctxStorageSizeKey struct{}
type ctxProjectKey struct{}
type ctxDenylistKey struct{}

type DenyList struct {
	Denylist string
}

func getDenylist(s ssh.Session) *DenyList {
	v := s.Context().Value(ctxDenylistKey{})
	if v == nil {
		return nil
	}
	denylist := s.Context().Value(ctxDenylistKey{}).(*DenyList)
	return denylist
}

func setDenylist(s ssh.Session, denylist string) {
	s.Context().SetValue(ctxDenylistKey{}, &DenyList{Denylist: denylist})
}

func getProject(s ssh.Session) *db.Project {
	v := s.Context().Value(ctxProjectKey{})
	if v == nil {
		return nil
	}
	project := s.Context().Value(ctxProjectKey{}).(*db.Project)
	return project
}

func setProject(s ssh.Session, project *db.Project) {
	s.Context().SetValue(ctxProjectKey{}, project)
}

func getBucket(s ssh.Session) (sst.Bucket, error) {
	bucket := s.Context().Value(ctxBucketKey{}).(sst.Bucket)
	if bucket.Name == "" {
		return bucket, fmt.Errorf("bucket not set on `ssh.Context()` for connection")
	}
	return bucket, nil
}

func getStorageSize(s ssh.Session) uint64 {
	return s.Context().Value(ctxStorageSizeKey{}).(uint64)
}

func incrementStorageSize(s ssh.Session, fileSize int64) uint64 {
	curSize := getStorageSize(s)
	var nextStorageSize uint64
	if fileSize < 0 {
		nextStorageSize = curSize - uint64(fileSize)
	} else {
		nextStorageSize = curSize + uint64(fileSize)
	}
	s.Context().SetValue(ctxStorageSizeKey{}, nextStorageSize)
	return nextStorageSize
}

func shouldIgnoreFile(fp, ignoreStr string) bool {
	object := ignore.CompileIgnoreLines(strings.Split(ignoreStr, "\n")...)
	return object.MatchesPath(fp)
}

type FileData struct {
	*utils.FileEntry
	Text          []byte
	User          *db.User
	Bucket        sst.Bucket
	StorageSize   uint64
	FeatureFlag   *db.FeatureFlag
	DeltaFileSize int64
	Project       *db.Project
	DenyList      string
}

type UploadAssetHandler struct {
	DBPool  db.DB
	Cfg     *shared.ConfigSite
	Storage storage.StorageServe
}

func NewUploadAssetHandler(dbpool db.DB, cfg *shared.ConfigSite, storage storage.StorageServe) *UploadAssetHandler {
	return &UploadAssetHandler{
		DBPool:  dbpool,
		Cfg:     cfg,
		Storage: storage,
	}
}

func (h *UploadAssetHandler) GetLogger() *slog.Logger {
	return h.Cfg.Logger
}

func (h *UploadAssetHandler) Read(s ssh.Session, entry *utils.FileEntry) (os.FileInfo, utils.ReaderAtCloser, error) {
	user, err := futil.GetUser(s)
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
	contents, size, modTime, err := h.Storage.GetObject(bucket, fname)
	if err != nil {
		return nil, nil, err
	}

	fileInfo.FSize = size
	fileInfo.FModTime = modTime

	reader := pobj.NewAllReaderAt(contents)

	return fileInfo, reader, nil
}

func (h *UploadAssetHandler) List(s ssh.Session, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error) {
	var fileList []os.FileInfo

	user, err := futil.GetUser(s)
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

		foundList, err := h.Storage.ListObjects(bucket, cleanFilename, recursive)
		if err != nil {
			return fileList, err
		}

		fileList = append(fileList, foundList...)
	}

	return fileList, nil
}

func (h *UploadAssetHandler) Validate(s ssh.Session) error {
	var err error
	key, err := shared.KeyText(s)
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

	ff, err := h.DBPool.FindFeatureForUser(user.ID, "plus")
	// pgs.sh has a free tier so users might not have a feature flag
	// in which case we set sane defaults
	if err != nil {
		ff = db.NewFeatureFlag(
			user.ID,
			"plus",
			h.Cfg.MaxSize,
			h.Cfg.MaxAssetSize,
		)
	}
	// this is jank
	ff.Data.StorageMax = ff.FindStorageMax(h.Cfg.MaxSize)
	ff.Data.FileMax = ff.FindFileMax(h.Cfg.MaxAssetSize)

	futil.SetFeatureFlag(s, ff)
	futil.SetUser(s, user)

	assetBucket := shared.GetAssetBucketName(user.ID)
	bucket, err := h.Storage.UpsertBucket(assetBucket)
	if err != nil {
		return err
	}
	s.Context().SetValue(ctxBucketKey{}, bucket)

	totalStorageSize, err := h.Storage.GetBucketQuota(bucket)
	if err != nil {
		return err
	}
	s.Context().SetValue(ctxStorageSizeKey{}, totalStorageSize)
	h.Cfg.Logger.Info(
		"bucket size",
		"user", user.Name,
		"bytes", totalStorageSize,
	)

	h.Cfg.Logger.Info(
		"attempting to upload files",
		"user", user.Name,
		"space", h.Cfg.Space,
	)

	return nil
}

func (h *UploadAssetHandler) findDenylist(bucket sst.Bucket, project *db.Project, logger *slog.Logger) (string, error) {
	fp, _, _, err := h.Storage.GetObject(bucket, filepath.Join(project.ProjectDir, "_pgs_ignore"))
	if err != nil {
		return "", fmt.Errorf("_pgs_ignore not found")
	}

	defer fp.Close()
	buf := new(strings.Builder)
	_, err = io.Copy(buf, fp)
	if err != nil {
		logger.Error("io copy", "err", err.Error())
		return "", err
	}

	str := buf.String()
	return str, nil
}

func (h *UploadAssetHandler) Write(s ssh.Session, entry *utils.FileEntry) (string, error) {
	user, err := futil.GetUser(s)
	if err != nil {
		h.Cfg.Logger.Error("user not found in ctx", "err", err.Error())
		return "", err
	}

	if entry.Mode.IsDir() && strings.Count(entry.Filepath, "/") == 1 {
		entry.Filepath = strings.TrimPrefix(entry.Filepath, "/")
	}

	logger := h.GetLogger().With(
		"user", user.Name,
		"file", entry.Filepath,
	)

	bucket, err := getBucket(s)
	if err != nil {
		logger.Error("could not find bucket in ctx", "err", err.Error())
		return "", err
	}

	project := getProject(s)
	projectName := shared.GetProjectName(entry)
	logger = logger.With("project", projectName)

	// find, create, or update project if we haven't already done it
	if project == nil {
		project, err = h.DBPool.FindProjectByName(user.ID, projectName)
		if err == nil {
			err = h.DBPool.UpdateProject(user.ID, projectName)
			if err != nil {
				logger.Error("could not update project", "err", err.Error())
				return "", err
			}
		} else {
			_, err = h.DBPool.InsertProject(user.ID, projectName, projectName)
			if err != nil {
				logger.Error("could not create project", "err", err.Error())
				return "", err
			}
			project, err = h.DBPool.FindProjectByName(user.ID, projectName)
			if err != nil {
				logger.Error("could not find project", "err", err.Error())
				return "", err
			}
		}
		setProject(s, project)
	}

	if entry.Mode.IsDir() {
		_, err := h.Storage.PutObject(
			bucket,
			path.Join(shared.GetAssetFileName(entry), "._pico_keep_dir"),
			utils.NopReaderAtCloser(bytes.NewReader([]byte{})),
			entry,
		)
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

	storageSize := getStorageSize(s)
	featureFlag, err := futil.GetFeatureFlag(s)
	if err != nil {
		return "", err
	}

	// calculate the filsize difference between the same file already
	// stored and the updated file being uploaded
	assetFilename := shared.GetAssetFileName(entry)
	curFileSize, _ := h.Storage.GetObjectSize(bucket, assetFilename)
	deltaFileSize := curFileSize - entry.Size

	denylist := getDenylist(s)
	if denylist == nil {
		dlist, err := h.findDenylist(bucket, project, logger)
		if err != nil {
			logger.Info("failed to get denylist, setting default (.*)", "err", err.Error())
			dlist = ".*"
		}
		setDenylist(s, dlist)
		denylist = &DenyList{Denylist: dlist}
	}

	data := &FileData{
		FileEntry:     entry,
		User:          user,
		Text:          origText,
		Bucket:        bucket,
		StorageSize:   storageSize,
		FeatureFlag:   featureFlag,
		DeltaFileSize: deltaFileSize,
		DenyList:      denylist.Denylist,
		Project:       project,
	}

	valid, err := h.validateAsset(data)
	if !valid {
		return "", err
	}

	err = h.writeAsset(data)
	if err != nil {
		logger.Error(err.Error())
		return "", err
	}
	nextStorageSize := incrementStorageSize(s, deltaFileSize)

	url := h.Cfg.AssetURL(
		user.Name,
		projectName,
		strings.Replace(data.Filepath, "/"+projectName+"/", "", 1),
	)

	maxSize := int(featureFlag.Data.StorageMax)
	str := fmt.Sprintf(
		"%s (space: %.2f/%.2fGB, %.2f%%)",
		url,
		shared.BytesToGB(int(nextStorageSize)),
		shared.BytesToGB(maxSize),
		(float32(nextStorageSize)/float32(maxSize))*100,
	)

	return str, nil
}

func (h *UploadAssetHandler) Delete(s ssh.Session, entry *utils.FileEntry) error {
	user, err := futil.GetUser(s)
	if err != nil {
		h.Cfg.Logger.Error("user not found in ctx", "err", err.Error())
		return err
	}

	if entry.Mode.IsDir() && strings.Count(entry.Filepath, "/") == 1 {
		entry.Filepath = strings.TrimPrefix(entry.Filepath, "/")
	}

	assetFilepath := shared.GetAssetFileName(entry)

	logger := h.GetLogger().With(
		"user", user.Name,
		"file", assetFilepath,
	)

	bucket, err := getBucket(s)
	if err != nil {
		logger.Error("could not find bucket in ctx", "err", err.Error())
		return err
	}

	projectName := shared.GetProjectName(entry)
	logger = logger.With("project", projectName)

	if assetFilepath == filepath.Join("/", projectName, "._pico_keep_dir") {
		return os.ErrPermission
	}

	logger.Info("deleting file")

	pathDir := filepath.Dir(assetFilepath)
	fileName := filepath.Base(assetFilepath)

	sibs, err := h.Storage.ListObjects(bucket, pathDir+"/", false)
	if err != nil {
		return err
	}

	sibs = slices.DeleteFunc(sibs, func(sib fs.FileInfo) bool {
		return sib.Name() == fileName
	})

	if len(sibs) == 0 {
		_, err := h.Storage.PutObject(
			bucket,
			filepath.Join(pathDir, "._pico_keep_dir"),
			utils.NopReaderAtCloser(bytes.NewReader([]byte{})),
			entry,
		)
		if err != nil {
			return err
		}
	}

	return h.Storage.DeleteObject(bucket, assetFilepath)
}

func (h *UploadAssetHandler) validateAsset(data *FileData) (bool, error) {
	storageMax := data.FeatureFlag.Data.StorageMax
	var nextStorageSize uint64
	if data.DeltaFileSize < 0 {
		nextStorageSize = data.StorageSize - uint64(data.DeltaFileSize)
	} else {
		nextStorageSize = data.StorageSize + uint64(data.DeltaFileSize)
	}
	if nextStorageSize >= storageMax {
		return false, fmt.Errorf(
			"ERROR: user (%s) has exceeded (%d bytes) max (%d bytes)",
			data.User.Name,
			data.StorageSize,
			storageMax,
		)
	}

	projectName := shared.GetProjectName(data.FileEntry)
	if projectName == "" || projectName == "/" || projectName == "." {
		return false, fmt.Errorf("ERROR: invalid project name, you must copy files to a non-root folder (e.g. pgs.sh:/project-name)")
	}

	fileSize := data.Size
	fname := filepath.Base(data.Filepath)
	fileMax := data.FeatureFlag.Data.FileMax
	if fileSize > fileMax {
		return false, fmt.Errorf("ERROR: file (%s) has exceeded maximum file size (%d bytes)", fname, fileMax)
	}

	// ".well-known" is a special case
	if strings.Contains(data.Filepath, "/.well-known/") {
		if shared.IsTextFile(string(data.Text)) {
			return true, nil
		} else {
			return false, fmt.Errorf("(%s) not a utf-8 text file", data.Filepath)
		}
	}

	// special files we use for custom routing
	if fname == "_pgs_ignore" || fname == "_redirects" || fname == "_headers" {
		return true, nil
	}

	fpath := strings.Replace(data.Filepath, "/"+projectName, "", 1)
	if shouldIgnoreFile(fpath, data.DenyList) {
		err := fmt.Errorf(
			"ERROR: (%s) file rejected, https://pico.sh/pgs#file-denylist",
			data.Filepath,
		)
		return false, err
	}

	return true, nil
}

func (h *UploadAssetHandler) writeAsset(data *FileData) error {
	assetFilepath := shared.GetAssetFileName(data.FileEntry)

	reader := bytes.NewReader(data.Text)

	h.Cfg.Logger.Info(
		"uploading file to bucket",
		"user", data.User.Name,
		"bucket", data.Bucket.Name,
		"filename", assetFilepath,
	)

	_, err := h.Storage.PutObject(
		data.Bucket,
		assetFilepath,
		utils.NopReaderAtCloser(reader),
		data.FileEntry,
	)
	if err != nil {
		return err
	}

	return nil
}

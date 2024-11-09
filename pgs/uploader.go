package pgs

import (
	"bytes"
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
	"github.com/charmbracelet/wish"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pobj"
	sst "github.com/picosh/pobj/storage"
	sendutils "github.com/picosh/send/utils"
	"github.com/picosh/utils"
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
	*sendutils.FileEntry
	User     *db.User
	Bucket   sst.Bucket
	Project  *db.Project
	DenyList string
}

type UploadAssetHandler struct {
	DBPool  db.DB
	Cfg     *shared.ConfigSite
	Storage sst.ObjectStorage
}

func NewUploadAssetHandler(dbpool db.DB, cfg *shared.ConfigSite, storage sst.ObjectStorage) *UploadAssetHandler {
	return &UploadAssetHandler{
		DBPool:  dbpool,
		Cfg:     cfg,
		Storage: storage,
	}
}

func (h *UploadAssetHandler) GetLogger() *slog.Logger {
	return h.Cfg.Logger
}

func (h *UploadAssetHandler) Read(s ssh.Session, entry *sendutils.FileEntry) (os.FileInfo, sendutils.ReaderAtCloser, error) {
	user, err := shared.GetUser(s.Context())
	if err != nil {
		return nil, nil, err
	}

	fileInfo := &sendutils.VirtualFile{
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
	contents, info, err := h.Storage.GetObject(bucket, fname)
	if err != nil {
		return nil, nil, err
	}

	fileInfo.FSize = info.Size
	fileInfo.FModTime = info.LastModified

	reader := pobj.NewAllReaderAt(contents)

	return fileInfo, reader, nil
}

func (h *UploadAssetHandler) List(s ssh.Session, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error) {
	var fileList []os.FileInfo

	user, err := shared.GetUser(s.Context())
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

		info := &sendutils.VirtualFile{
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
	user, err := shared.GetUser(s.Context())
	if err != nil {
		return err
	}

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
	fp, _, err := h.Storage.GetObject(bucket, filepath.Join(project.ProjectDir, "_pgs_ignore"))
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

func (h *UploadAssetHandler) Write(s ssh.Session, entry *sendutils.FileEntry) (string, error) {
	user, err := shared.GetUser(s.Context())
	if user == nil || err != nil {
		h.Cfg.Logger.Error("user not found in ctx", "err", err.Error())
		return "", err
	}

	if entry.Mode.IsDir() && strings.Count(entry.Filepath, "/") == 1 {
		entry.Filepath = strings.TrimPrefix(entry.Filepath, "/")
	}

	logger := h.GetLogger()
	logger = shared.LoggerWithUser(logger, user)
	logger = logger.With(
		"file", entry.Filepath,
		"size", entry.Size,
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

	if project.Blocked != "" {
		msg := "project has been blocked and cannot upload files: %s"
		return "", fmt.Errorf(msg, project.Blocked)
	}

	if entry.Mode.IsDir() {
		_, _, err := h.Storage.PutObject(
			bucket,
			path.Join(shared.GetAssetFileName(entry), "._pico_keep_dir"),
			bytes.NewReader([]byte{}),
			entry,
		)
		return "", err
	}

	featureFlag, err := shared.GetFeatureFlag(s.Context())
	if err != nil {
		return "", err
	}

	// calculate the filsize difference between the same file already
	// stored and the updated file being uploaded
	assetFilename := shared.GetAssetFileName(entry)
	_, info, _ := h.Storage.GetObject(bucket, assetFilename)
	var curFileSize int64
	if info != nil {
		curFileSize = info.Size
	}

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
		FileEntry: entry,
		User:      user,
		Bucket:    bucket,
		DenyList:  denylist.Denylist,
		Project:   project,
	}

	valid, err := h.validateAsset(data)
	if !valid {
		return "", err
	}

	// SFTP does not report file size so the more performant way to
	//   check filesize constraints is to try and upload the file to s3
	//	 with a specialized reader that raises an error if the filesize limit
	//	 has been reached
	storageMax := featureFlag.Data.StorageMax
	fileMax := featureFlag.Data.FileMax
	curStorageSize := getStorageSize(s)
	remaining := int64(storageMax) - int64(curStorageSize)
	sizeRemaining := min(remaining+curFileSize, fileMax)
	if sizeRemaining <= 0 {
		wish.Fatalln(s, "storage quota reached")
		return "", fmt.Errorf("storage quota reached")
	}
	logger = logger.With(
		"storageMax", storageMax,
		"currentStorageMax", curStorageSize,
		"fileMax", fileMax,
		"sizeRemaining", sizeRemaining,
	)

	specialFileMax := featureFlag.Data.SpecialFileMax
	if isSpecialFile(entry) {
		sizeRemaining = min(sizeRemaining, specialFileMax)
	}

	fsize, err := h.writeAsset(
		utils.NewMaxBytesReader(data.Reader, int64(sizeRemaining)),
		data,
	)
	if err != nil {
		logger.Error("could not write asset", "err", err.Error())
		cerr := fmt.Errorf(
			"%s: storage size %.2fmb, storage max %.2fmb, file max %.2fmb, special file max %.4fmb",
			err,
			utils.BytesToMB(int(curStorageSize)),
			utils.BytesToMB(int(storageMax)),
			utils.BytesToMB(int(fileMax)),
			utils.BytesToMB(int(specialFileMax)),
		)
		return "", cerr
	}

	deltaFileSize := curFileSize - fsize
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
		utils.BytesToGB(int(nextStorageSize)),
		utils.BytesToGB(maxSize),
		(float32(nextStorageSize)/float32(maxSize))*100,
	)

	return str, nil
}

func isSpecialFile(entry *sendutils.FileEntry) bool {
	fname := filepath.Base(entry.Filepath)
	return fname == "_headers" || fname == "_redirects"
}

func (h *UploadAssetHandler) Delete(s ssh.Session, entry *sendutils.FileEntry) error {
	user, err := shared.GetUser(s.Context())
	if err != nil {
		h.Cfg.Logger.Error("user not found in ctx", "err", err.Error())
		return err
	}

	if entry.Mode.IsDir() && strings.Count(entry.Filepath, "/") == 1 {
		entry.Filepath = strings.TrimPrefix(entry.Filepath, "/")
	}

	assetFilepath := shared.GetAssetFileName(entry)

	logger := h.GetLogger()
	logger = shared.LoggerWithUser(logger, user)
	logger = logger.With(
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
		_, _, err := h.Storage.PutObject(
			bucket,
			filepath.Join(pathDir, "._pico_keep_dir"),
			bytes.NewReader([]byte{}),
			entry,
		)
		if err != nil {
			return err
		}
	}

	return h.Storage.DeleteObject(bucket, assetFilepath)
}

func (h *UploadAssetHandler) validateAsset(data *FileData) (bool, error) {
	fname := filepath.Base(data.Filepath)

	projectName := shared.GetProjectName(data.FileEntry)
	if projectName == "" || projectName == "/" || projectName == "." {
		return false, fmt.Errorf("ERROR: invalid project name, you must copy files to a non-root folder (e.g. pgs.sh:/project-name)")
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

func (h *UploadAssetHandler) writeAsset(reader io.Reader, data *FileData) (int64, error) {
	assetFilepath := shared.GetAssetFileName(data.FileEntry)

	logger := shared.LoggerWithUser(h.Cfg.Logger, data.User)
	logger.Info(
		"uploading file to bucket",
		"bucket", data.Bucket.Name,
		"filename", assetFilepath,
	)

	_, fsize, err := h.Storage.PutObject(
		data.Bucket,
		assetFilepath,
		reader,
		data.FileEntry,
	)
	return fsize, err
}

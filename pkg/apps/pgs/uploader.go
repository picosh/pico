package pgs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/pssh"
	sendutils "github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/storage"
	ignore "github.com/sabhiram/go-gitignore"
)

type ctxUploadStateKey struct{}

// uploadState is what a session's uploads share. It is set on the session
// once because every SetValue nests the session context one level deeper,
// and SFTP can run several writes at a time.
type uploadState struct {
	mu          sync.Mutex
	bucket      storage.Bucket
	featureFlag *db.FeatureFlag
	storageSize int64
	project     *db.Project
	denylist    *ignore.GitIgnore
}

func getUploadState(s *pssh.SSHServerConnSession) (*uploadState, error) {
	state, ok := s.Context().Value(ctxUploadStateKey{}).(*uploadState)
	if !ok {
		return nil, fmt.Errorf("upload state not set on `ssh.Context()` for connection")
	}
	return state, nil
}

func (u *uploadState) getStorageSize() int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.storageSize
}

// addStorageSize adds delta bytes to the bucket's size and returns the new
// size.
func (u *uploadState) addStorageSize(delta int64) int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.storageSize = max(u.storageSize+delta, 0)
	return u.storageSize
}

type FileData struct {
	*sendutils.FileEntry
	User     *db.User
	Bucket   storage.Bucket
	Project  *db.Project
	DenyList *ignore.GitIgnore
}

type UploadAssetHandler struct {
	Cfg                *PgsConfig
	CacheClearingQueue chan string
}

func NewUploadAssetHandler(cfg *PgsConfig, ch chan string, ctx context.Context) *UploadAssetHandler {
	go runCacheQueue(cfg, ctx)
	return &UploadAssetHandler{
		Cfg:                cfg,
		CacheClearingQueue: ch,
	}
}

func (h *UploadAssetHandler) GetLogger(s *pssh.SSHServerConnSession) *slog.Logger {
	return pssh.GetLogger(s)
}

func (h *UploadAssetHandler) Read(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) (os.FileInfo, sendutils.ReadAndReaderAtCloser, error) {
	logger := pssh.GetLogger(s)
	user := pssh.GetUser(s)

	if user == nil {
		err := fmt.Errorf("could not get user from ctx")
		logger.Error("error getting user from ctx", "err", err)
		return nil, nil, err
	}

	fileInfo := &sendutils.VirtualFile{
		FName:    filepath.Base(entry.Filepath),
		FIsDir:   false,
		FSize:    entry.Size,
		FModTime: time.Unix(entry.Mtime, 0),
	}

	bucket, err := h.Cfg.Storage.GetBucket(shared.GetAssetBucketName(user.ID))
	if err != nil {
		return nil, nil, err
	}

	fname := shared.GetAssetFileName(entry)
	contents, info, err := h.Cfg.Storage.GetObject(bucket, fname)
	if err != nil {
		return nil, nil, err
	}

	fileInfo.FSize = info.Size
	fileInfo.FModTime = info.LastModified

	return fileInfo, contents, nil
}

func (h *UploadAssetHandler) List(s *pssh.SSHServerConnSession, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error) {
	var fileList []os.FileInfo

	logger := pssh.GetLogger(s)
	user := pssh.GetUser(s)

	if user == nil {
		err := fmt.Errorf("could not get user from ctx")
		logger.Error("error getting user from ctx", "err", err)
		return fileList, err
	}

	cleanFilename := fpath

	bucketName := shared.GetAssetBucketName(user.ID)
	bucket, err := h.Cfg.Storage.GetBucket(bucketName)
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

		foundList, err := h.Cfg.Storage.ListObjects(bucket, cleanFilename, recursive)
		if err != nil {
			return fileList, err
		}

		fileList = append(fileList, foundList...)
	}

	return fileList, nil
}

func (h *UploadAssetHandler) Validate(s *pssh.SSHServerConnSession) error {
	logger := pssh.GetLogger(s)
	user := pssh.GetUser(s)

	if user == nil {
		err := fmt.Errorf("could not get user from ctx")
		logger.Error("error getting user from ctx", "err", err)
		return err
	}

	ff, err := findFeatureFlag(h.Cfg.DB, h.Cfg, user.ID)
	if err != nil {
		return err
	}

	assetBucket := shared.GetAssetBucketName(user.ID)
	bucket, err := h.Cfg.Storage.UpsertBucket(assetBucket)
	if err != nil {
		return err
	}

	totalStorageSize, err := h.Cfg.Storage.GetBucketQuota(bucket)
	if err != nil {
		return err
	}
	s.SetValue(ctxUploadStateKey{}, &uploadState{
		bucket:      bucket,
		featureFlag: ff,
		storageSize: int64(totalStorageSize),
	})

	logger.Info(
		"bucket size",
		"user", user.Name,
		"bytes", totalStorageSize,
	)

	logger.Info(
		"attempting to upload files",
		"user", user.Name,
		"txtPrefix", h.Cfg.TxtPrefix,
	)

	return nil
}

// sessionProject returns the project being uploaded to and its compiled
// _pgs_ignore, finding or creating the project when it changes. A session
// can upload to many projects because SFTP connections are kept alive.
func (h *UploadAssetHandler) sessionProject(state *uploadState, user *db.User, projectName string, logger *slog.Logger) (*db.Project, *ignore.GitIgnore, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.project != nil && state.project.Name == projectName {
		return state.project, state.denylist, nil
	}

	project, err := h.Cfg.DB.UpsertProject(user.ID, projectName, projectName)
	if err != nil {
		logger.Error("upsert project", "err", err.Error())
		return nil, nil, err
	}

	dlist, err := h.findDenylist(state.bucket, project, logger)
	if err != nil {
		logger.Info("failed to get denylist, setting default (.*)", "err", err.Error())
		dlist = ".*"
	}

	state.project = project
	state.denylist = ignore.CompileIgnoreLines(strings.Split(dlist, "\n")...)
	return state.project, state.denylist, nil
}

func (h *UploadAssetHandler) findDenylist(bucket storage.Bucket, project *db.Project, logger *slog.Logger) (string, error) {
	fp, _, err := h.Cfg.Storage.GetObject(bucket, filepath.Join(project.ProjectDir, "_pgs_ignore"))
	if err != nil {
		return "", fmt.Errorf("_pgs_ignore not found")
	}
	defer func() {
		_ = fp.Close()
	}()

	buf := new(strings.Builder)
	_, err = io.Copy(buf, fp)
	if err != nil {
		logger.Error("io copy", "err", err.Error())
		return "", err
	}

	str := buf.String()
	return str, nil
}

func mtimeToTime(entry *sendutils.FileEntry) time.Time {
	var mtime time.Time
	if entry.Mtime > 0 {
		return time.Unix(entry.Mtime, 0)
	}
	return mtime
}

func (h *UploadAssetHandler) Write(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) (string, error) {
	logger := pssh.GetLogger(s)
	user := pssh.GetUser(s)

	if user == nil {
		err := fmt.Errorf("could not get user from ctx")
		logger.Error("error getting user from ctx", "err", err)
		return "", err
	}

	if entry.Mode.IsDir() && strings.Count(entry.Filepath, "/") == 1 {
		entry.Filepath = strings.TrimPrefix(entry.Filepath, "/")
	}

	logger = logger.With(
		"file", entry.Filepath,
		"size", entry.Size,
	)

	state, err := getUploadState(s)
	if err != nil {
		logger.Error("could not find upload state in ctx", "err", err.Error())
		return "", err
	}
	bucket := state.bucket

	projectName := shared.GetProjectName(entry)
	logger = logger.With("project", projectName)

	project, denylist, err := h.sessionProject(state, user, projectName, logger)
	if err != nil {
		return "", err
	}

	if project.Blocked != "" {
		msg := "project has been blocked and cannot upload files: %s"
		return "", fmt.Errorf(msg, project.Blocked)
	}

	if entry.Mode.IsDir() {
		return "", h.Cfg.Storage.PutDir(bucket, shared.GetAssetFileName(entry))
	}

	// calculate the filsize difference between the same file already
	// stored and the updated file being uploaded
	assetFilename := shared.GetAssetFileName(entry)
	var curFileSize int64
	if info, err := h.Cfg.Storage.StatObject(bucket, assetFilename); err == nil {
		curFileSize = info.Size
	}

	data := &FileData{
		FileEntry: entry,
		User:      user,
		Bucket:    bucket,
		DenyList:  denylist,
		Project:   project,
	}

	valid, err := h.validateAsset(data)
	if !valid {
		return "", err
	}

	featureFlag := state.featureFlag
	// SFTP does not report file size so the more performant way to
	//   check filesize constraints is to try and upload the file to s3
	//	 with a specialized reader that raises an error if the filesize limit
	//	 has been reached
	storageMax := featureFlag.Data.StorageMax
	fileMax := featureFlag.Data.FileMax
	curStorageSize := state.getStorageSize()
	remaining := int64(storageMax) - curStorageSize
	sizeRemaining := min(remaining+curFileSize, fileMax)
	if sizeRemaining <= 0 {
		_, _ = fmt.Fprintln(s.Stderr(), "storage quota reached")
		_, _ = fmt.Fprintf(s.Stderr(), "\r")
		_ = s.Exit(1)
		_ = s.Close()
		return "", fmt.Errorf("storage quota reached")
	}
	logger = logger.With(
		"storageMax", storageMax,
		"currentStorageMax", curStorageSize,
		"fileMax", fileMax,
		"sizeRemaining", sizeRemaining,
	)

	specialFileMax := featureFlag.Data.SpecialFileMax
	if isSpecialFile(entry.Filepath) {
		sizeRemaining = min(sizeRemaining, specialFileMax)
	}

	fsize, err := h.writeAsset(
		s,
		shared.NewMaxBytesReader(data.Reader, int64(sizeRemaining)),
		data,
	)
	if err != nil {
		logger.Error("could not write asset", "err", err.Error())
		cerr := fmt.Errorf(
			"%s: storage size %.2fmb, storage max %.2fmb, file max %.2fmb, special file max %.4fmb",
			err,
			shared.BytesToMB(int(curStorageSize)),
			shared.BytesToMB(int(storageMax)),
			shared.BytesToMB(int(fileMax)),
			shared.BytesToMB(int(specialFileMax)),
		)
		return "", cerr
	}

	nextStorageSize := state.addStorageSize(fsize - curFileSize)

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

	surrogate := getSurrogateKey(user.Name, projectName)
	h.Cfg.CacheClearingQueue <- surrogate

	return str, err
}

func isSpecialFile(entry string) bool {
	fname := filepath.Base(entry)
	return fname == "_headers" || fname == "_redirects" || fname == "_pgs_ignore"
}

func (h *UploadAssetHandler) Delete(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) error {
	logger := pssh.GetLogger(s)
	user := pssh.GetUser(s)

	if user == nil {
		err := fmt.Errorf("could not get user from ctx")
		logger.Error("error getting user from ctx", "err", err)
		return err
	}

	if entry.Mode.IsDir() && strings.Count(entry.Filepath, "/") == 1 {
		entry.Filepath = strings.TrimPrefix(entry.Filepath, "/")
	}

	assetFilepath := shared.GetAssetFileName(entry)

	logger = logger.With(
		"file", assetFilepath,
	)

	state, err := getUploadState(s)
	if err != nil {
		logger.Error("could not find upload state in ctx", "err", err.Error())
		return err
	}
	bucket := state.bucket

	projectName := shared.GetProjectName(entry)
	logger = logger.With("project", projectName)

	logger.Info("deleting file")

	info, statErr := h.Cfg.Storage.StatObject(bucket, assetFilepath)
	err = h.Cfg.Storage.DeleteObject(bucket, assetFilepath)
	if err == nil && statErr == nil {
		state.addStorageSize(-info.Size)
	}

	surrogate := getSurrogateKey(user.Name, projectName)
	h.Cfg.CacheClearingQueue <- surrogate

	if err != nil {
		return err
	}

	return err
}

func (h *UploadAssetHandler) validateAsset(data *FileData) (bool, error) {
	fname := filepath.Base(data.Filepath)

	projectName := shared.GetProjectName(data.FileEntry)
	if projectName == "" || projectName == "/" || projectName == "." {
		return false, fmt.Errorf("ERROR: invalid project name, you must copy files to a non-root folder (e.g. pgs.sh:/project-name)")
	}

	// special files we use for custom routing
	if isSpecialFile(fname) {
		return true, nil
	}

	fpath := strings.Replace(data.Filepath, "/"+projectName, "", 1)
	if data.DenyList.MatchesPath(fpath) {
		err := fmt.Errorf(
			"ERROR: (%s) file rejected, https://pico.sh/pgs#-pgs-ignore",
			data.Filepath,
		)
		return false, err
	}

	return true, nil
}

func (h *UploadAssetHandler) writeAsset(s *pssh.SSHServerConnSession, reader io.Reader, data *FileData) (int64, error) {
	assetFilepath := shared.GetAssetFileName(data.FileEntry)

	logger := h.GetLogger(s)
	logger.Info(
		"uploading file to bucket",
		"bucket", data.Bucket.Name,
		"filename", assetFilepath,
	)

	info := &storage.ObjectInfo{
		LastModified: mtimeToTime(data.FileEntry),
	}
	_, fsize, err := h.Cfg.Storage.PutObject(
		data.Bucket,
		assetFilepath,
		reader,
		info,
	)
	return fsize, err
}

// runCacheQueue processes requests to purge the cache for a single site.
// One message arrives per file that is written/deleted during uploads.
// Repeated messages for the same site are grouped so that we only flush once
// per site per 5 seconds.
func runCacheQueue(cfg *PgsConfig, ctx context.Context) {
	var pendingFlushes sync.Map
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case host := <-cfg.CacheClearingQueue:
			pendingFlushes.Store(host, host)
		case <-tick.C:
			go func() {
				pendingFlushes.Range(func(key, value any) bool {
					pendingFlushes.Delete(key)
					err := purgeCache(cfg, cfg.Pubsub, key.(string))
					if err != nil {
						cfg.Logger.Error("failed to clear cache", "err", err.Error())
					}
					return true
				})
			}()
		}
	}
}

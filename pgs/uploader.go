package pgs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/picosh/pico/db"
	pgsdb "github.com/picosh/pico/pgs/db"
	"github.com/picosh/pico/pssh"
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

func getDenylist(s *pssh.SSHServerConnSession) *DenyList {
	v := s.Context().Value(ctxDenylistKey{})
	if v == nil {
		return nil
	}
	denylist := s.Context().Value(ctxDenylistKey{}).(*DenyList)
	return denylist
}

func setDenylist(s *pssh.SSHServerConnSession, denylist string) {
	s.SetValue(ctxDenylistKey{}, &DenyList{Denylist: denylist})
}

func getProject(s *pssh.SSHServerConnSession) *db.Project {
	v := s.Context().Value(ctxProjectKey{})
	if v == nil {
		return nil
	}
	project := s.Context().Value(ctxProjectKey{}).(*db.Project)
	return project
}

func setProject(s *pssh.SSHServerConnSession, project *db.Project) {
	s.SetValue(ctxProjectKey{}, project)
}

func getBucket(s *pssh.SSHServerConnSession) (sst.Bucket, error) {
	bucket := s.Context().Value(ctxBucketKey{}).(sst.Bucket)
	if bucket.Name == "" {
		return bucket, fmt.Errorf("bucket not set on `ssh.Context()` for connection")
	}
	return bucket, nil
}

func getStorageSize(s *pssh.SSHServerConnSession) uint64 {
	return s.Context().Value(ctxStorageSizeKey{}).(uint64)
}

func incrementStorageSize(s *pssh.SSHServerConnSession, fileSize int64) uint64 {
	curSize := getStorageSize(s)
	var nextStorageSize uint64
	if fileSize < 0 {
		nextStorageSize = curSize - uint64(fileSize)
	} else {
		nextStorageSize = curSize + uint64(fileSize)
	}
	s.SetValue(ctxStorageSizeKey{}, nextStorageSize)
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

	reader := pobj.NewAllReaderAt(contents)

	return fileInfo, reader, nil
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

	assetBucket := shared.GetAssetBucketName(user.ID)
	bucket, err := h.Cfg.Storage.UpsertBucket(assetBucket)
	if err != nil {
		return err
	}

	s.SetValue(ctxBucketKey{}, bucket)

	totalStorageSize, err := h.Cfg.Storage.GetBucketQuota(bucket)
	if err != nil {
		return err
	}

	s.SetValue(ctxStorageSizeKey{}, totalStorageSize)

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

func (h *UploadAssetHandler) findDenylist(bucket sst.Bucket, project *db.Project, logger *slog.Logger) (string, error) {
	fp, _, err := h.Cfg.Storage.GetObject(bucket, filepath.Join(project.ProjectDir, "_pgs_ignore"))
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

func findPlusFF(dbpool pgsdb.PgsDB, cfg *PgsConfig, userID string) *db.FeatureFlag {
	ff, _ := dbpool.FindFeature(userID, "plus")
	// we have free tiers so users might not have a feature flag
	// in which case we set sane defaults
	if ff == nil {
		ff = db.NewFeatureFlag(
			userID,
			"plus",
			cfg.MaxSize,
			cfg.MaxAssetSize,
			cfg.MaxSpecialFileSize,
		)
	}
	// this is jank
	ff.Data.StorageMax = ff.FindStorageMax(cfg.MaxSize)
	ff.Data.FileMax = ff.FindFileMax(cfg.MaxAssetSize)
	ff.Data.SpecialFileMax = ff.FindSpecialFileMax(cfg.MaxSpecialFileSize)
	return ff
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
		project, err = h.Cfg.DB.UpsertProject(user.ID, projectName, projectName)
		if err != nil {
			logger.Error("upsert project", "err", err.Error())
			return "", err
		}
		setProject(s, project)
	}

	if project.Blocked != "" {
		msg := "project has been blocked and cannot upload files: %s"
		return "", fmt.Errorf(msg, project.Blocked)
	}

	if entry.Mode.IsDir() {
		_, _, err := h.Cfg.Storage.PutObject(
			bucket,
			path.Join(shared.GetAssetFileName(entry), "._pico_keep_dir"),
			bytes.NewReader([]byte{}),
			entry,
		)
		return "", err
	}

	featureFlag := findPlusFF(h.Cfg.DB, h.Cfg, user.ID)
	// calculate the filsize difference between the same file already
	// stored and the updated file being uploaded
	assetFilename := shared.GetAssetFileName(entry)
	obj, info, _ := h.Cfg.Storage.GetObject(bucket, assetFilename)
	var curFileSize int64
	if info != nil {
		curFileSize = info.Size
	}
	if obj != nil {
		defer obj.Close()
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
		fmt.Fprintln(s.Stderr(), "storage quota reached")
		fmt.Fprintf(s.Stderr(), "\r")
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
	if isSpecialFile(entry) {
		sizeRemaining = min(sizeRemaining, specialFileMax)
	}

	fsize, err := h.writeAsset(
		s,
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

	surrogate := getSurrogateKey(user.Name, projectName)
	h.Cfg.CacheClearingQueue <- surrogate

	return str, err
}

func isSpecialFile(entry *sendutils.FileEntry) bool {
	fname := filepath.Base(entry.Filepath)
	return fname == "_headers" || fname == "_redirects"
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

	sibs, err := h.Cfg.Storage.ListObjects(bucket, pathDir+"/", false)
	if err != nil {
		return err
	}

	sibs = slices.DeleteFunc(sibs, func(sib fs.FileInfo) bool {
		return sib.Name() == fileName
	})

	if len(sibs) == 0 {
		_, _, err := h.Cfg.Storage.PutObject(
			bucket,
			filepath.Join(pathDir, "._pico_keep_dir"),
			bytes.NewReader([]byte{}),
			entry,
		)
		if err != nil {
			return err
		}
	}
	err = h.Cfg.Storage.DeleteObject(bucket, assetFilepath)

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
	if fname == "_pgs_ignore" || fname == "_redirects" || fname == "_headers" {
		return true, nil
	}

	fpath := strings.Replace(data.Filepath, "/"+projectName, "", 1)
	if shouldIgnoreFile(fpath, data.DenyList) {
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

	_, fsize, err := h.Cfg.Storage.PutObject(
		data.Bucket,
		assetFilepath,
		reader,
		data.FileEntry,
	)
	return fsize, err
}

// runCacheQueue processes requests to purge the cache for a single site.
// One message arrives per file that is written/deleted during uploads.
// Repeated messages for the same site are grouped so that we only flush once
// per site per 5 seconds.
func runCacheQueue(cfg *PgsConfig, ctx context.Context) {
	send := createPubCacheDrain(ctx, cfg.Logger)
	var pendingFlushes sync.Map
	tick := time.Tick(5 * time.Second)
	for {
		select {
		case host := <-cfg.CacheClearingQueue:
			pendingFlushes.Store(host, host)
		case <-tick:
			go func() {
				pendingFlushes.Range(func(key, value any) bool {
					pendingFlushes.Delete(key)
					err := purgeCache(cfg, send, key.(string))
					if err != nil {
						cfg.Logger.Error("failed to clear cache", "err", err.Error())
					}
					return true
				})
			}()
		}
	}
}

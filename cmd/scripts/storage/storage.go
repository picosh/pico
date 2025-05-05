package main

import (
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared/storage"
)

// This script will use whatever storage adapter is set by the env var
// `STORAGE_TYPE`.  These tests will *not* confirm the adapter is working properly
// beyond calling all of the methods and ensuring no errors are returned.
// It is up to you to go into the adapter GUI and confirm the following:
//   - Only 1 bucket remains: "test"
//   - Only 1 object is inside the folder: here/we/go/again.txt"
func main() {
	logger := slog.Default()
	f, err := os.MkdirTemp("", "fs-tests-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(f)

	st, err := storage.NewStorage(logger)
	if err != nil {
		panic(err)
	}

	bucketName := "test"
	// create bucket
	bucket, err := st.UpsertBucket(bucketName)
	if err != nil {
		panic(err)
	}

	bucketCheck, err := st.GetBucket(bucketName)
	if err != nil {
		panic(err)
	}
	if bucketCheck.Path != bucket.Path || bucketCheck.Name != bucket.Name {
		panic("upsert and get bucket incongruent")
	}

	modTime := time.Now()

	str := "here is a test file"
	reader := strings.NewReader(str)
	actualPath, size, err := st.PutObject(bucket, "/nice/test.txt", reader, &utils.FileEntry{
		Mtime: modTime.Unix(),
	})
	if err != nil {
		panic(err)
	}
	if size != int64(len(str)) {
		panic(fmt.Sprintf("size, actual: %d, expected: %d", size, int64(len(str))))
	}
	expectedPath := filepath.Join(bucket.Name, "nice", "test.txt")
	if actualPath != expectedPath {
		panic(fmt.Sprintf("path, actual: %s, expected: %s", actualPath, expectedPath))
	}

	// get file
	r, info, err := st.GetObject(bucket, "/nice/test.txt")
	if err != nil {
		panic(err)
	}
	buf := new(strings.Builder)
	_, err = io.Copy(buf, r)
	if err != nil {
		panic(err)
	}
	actualStr := buf.String()
	if actualStr != str {
		panic(fmt.Sprintf("contents, actual: %s, expected: %s", actualStr, str))
	}
	if info.Size != size {
		panic(fmt.Sprintf("size, actual: %d, expected: %d", size, info.Size))
	}

	str = "a deeply nested test file"
	reader = strings.NewReader(str)
	_, _, err = st.PutObject(bucket, "/here/we/go/again.txt", reader, &utils.FileEntry{
		Mtime: modTime.Unix(),
	})
	if err != nil {
		panic(err)
	}

	// list objects
	objs, err := st.ListObjects(bucket, "/", true)
	if err != nil {
		panic(err)
	}

	expectedObjs := []fs.FileInfo{
		&utils.VirtualFile{FName: "here/we/go/again.txt", FSize: 25},
		&utils.VirtualFile{FName: "nice/test.txt", FSize: 19},
	}
	ignore := cmpopts.IgnoreFields(utils.VirtualFile{}, "FModTime", "FSize")
	if cmp.Equal(objs, expectedObjs, ignore) == false {
		//nolint
		panic(cmp.Diff(objs, expectedObjs, ignore))
	}

	// list buckets
	aBucket, _ := st.UpsertBucket("another")
	bBucket, _ := st.UpsertBucket("and-another")
	buckets, err := st.ListBuckets()
	if err != nil {
		panic(err)
	}
	expectedBuckets := []string{"and-another", "another", "main"}
	notFound := ""
	for _, buck := range expectedBuckets {
		if !slices.Contains(buckets, buck) {
			notFound = buck
			break
		}
	}
	if notFound != "" {
		panic(fmt.Sprintf("bucket not found: %s", notFound))
	}

	// delete bucket
	err = st.DeleteBucket(aBucket)
	if err != nil {
		panic(err)
	}

	err = st.DeleteObject(bucket, "nice/test.txt")
	if err != nil {
		panic(err)
	}

	str = "a deeply nested test file"
	reader = strings.NewReader(str)
	_, _, err = st.PutObject(bucket, "/here/yes/we/can.txt", reader, &utils.FileEntry{
		Mtime: modTime.Unix(),
	})
	if err != nil {
		panic(err)
	}

	// delete deeply nested file and all parent folders that are now empty
	err = st.DeleteObject(bucket, "/here/yes/we/can.txt")
	if err != nil {
		panic(err)
	}

	// delete bucket
	err = st.DeleteBucket(bBucket)
	if err != nil {
		panic(err)
	}
}

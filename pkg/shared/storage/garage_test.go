package storage

import (
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/picosh/pico/pkg/send/utils"
)

func TestGarageAdapter(t *testing.T) {
	logger := slog.Default()
	f, err := os.MkdirTemp("", "fs-tests-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(f)

	st, err := NewStorage(logger)
	if err != nil {
		t.Fatal(err)
	}

	bucketName := "main-v4"
	// create bucket
	bucket, err := st.UpsertBucket(bucketName)
	if err != nil {
		t.Fatal(err)
	}

	// ensure bucket exists
	/* file, err := os.Stat(bucket.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !file.IsDir() {
		t.Fatal("bucket must be directory")
	} */

	bucketCheck, err := st.GetBucket(bucketName)
	if err != nil {
		t.Fatal(err)
	}
	if bucketCheck.Path != bucket.Path || bucketCheck.Name != bucket.Name {
		t.Fatal("upsert and get bucket incongruent")
	}

	modTime := time.Now()

	str := "here is a test file"
	reader := strings.NewReader(str)
	actualPath, size, err := st.PutObject(bucket, "/nice/test.txt", reader, &utils.FileEntry{
		Mtime: modTime.Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(str)) {
		t.Fatalf("size, actual: %d, expected: %d", size, int64(len(str)))
	}
	expectedPath := filepath.Join(bucket.Name, "nice", "test.txt")
	if actualPath != expectedPath {
		t.Fatalf("path, actual: %s, expected: %s", actualPath, expectedPath)
	}

	// ensure file exists
	/* _, err = os.Stat(expectedPath)
	if err != nil {
		t.Fatal(err)
	} */

	// get file
	r, info, err := st.GetObject(bucket, "nice/test.txt")
	if err != nil {
		t.Fatal(err)
	}
	buf := new(strings.Builder)
	_, err = io.Copy(buf, r)
	if err != nil {
		t.Fatal(err)
	}
	actualStr := buf.String()
	if actualStr != str {
		t.Fatalf("contents, actual: %s, expected: %s", actualStr, str)
	}
	if info.Size != size {
		t.Fatalf("size, actual: %d, expected: %d", size, info.Size)
	}

	str = "a deeply nested test file"
	reader = strings.NewReader(str)
	_, _, err = st.PutObject(bucket, "/here/we/go/again.txt", reader, &utils.FileEntry{
		Mtime: modTime.Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// list objects
	objs, err := st.ListObjects(bucket, "/", true)
	if err != nil {
		t.Fatal(err)
	}

	expectedObjs := []fs.FileInfo{
		&utils.VirtualFile{FName: "here/we/go/again.txt", FSize: 25},
		&utils.VirtualFile{FName: "nice/test.txt", FSize: 19},
	}
	ignore := cmpopts.IgnoreFields(utils.VirtualFile{}, "FModTime", "FSize")
	if cmp.Equal(objs, expectedObjs, ignore) == false {
		//nolint
		t.Fatal(cmp.Diff(objs, expectedObjs, ignore))
	}

	// list buckets
	aBucket, _ := st.UpsertBucket("another")
	_, _ = st.UpsertBucket("and-another")
	buckets, err := st.ListBuckets()
	if err != nil {
		t.Fatal(err)
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
		t.Fatal(fmt.Sprintf("bucket not found: %s", notFound))
	}

	// delete bucket
	err = st.DeleteBucket(aBucket)
	if err != nil {
		t.Fatal(err)
	}

	// ensure bucket was actually deleted
	/* _, err = os.Stat(aBucket.Path)
	if !os.IsNotExist(err) {
		t.Fatal("directory should have been deleted")
	} */

	err = st.DeleteObject(bucket, "nice/test.txt")
	if err != nil {
		t.Fatal(err)
	}

	// ensure file was actually deleted
	/* _, err = os.Stat(filepath.Join(bucket.Path, "nice/test.txt"))
	if !os.IsNotExist(err) {
		t.Fatal("file should have been deleted")
	} */

	// ensure containing folder was also deleted
	/* _, err = os.Stat(filepath.Join(bucket.Path, "nice"))
	if !os.IsNotExist(err) {
		t.Fatal("containing folder should have been deleted")
	} */

	str = "a deeply nested test file"
	reader = strings.NewReader(str)
	_, _, err = st.PutObject(bucket, "/here/yes/we/can.txt", reader, &utils.FileEntry{
		Mtime: modTime.Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// delete deeply nested file and all parent folders that are now empty
	err = st.DeleteObject(bucket, "here/yes/we/can.txt")
	if err != nil {
		t.Fatal(err)
	}
	/* _, err = os.Stat(filepath.Join(bucket.Path, "here"))
	if os.IsNotExist(err) {
		t.Fatal("this folder had multiple files and should not have been deleted")
	}
	_, err = os.Stat(filepath.Join(bucket.Path, "here/yes"))
	if !os.IsNotExist(err) {
		t.Fatal("containing folder should have been deleted")
	} */

	// delete bucket even with file contents
	/* err = st.DeleteBucket(bucket)
	if err != nil {
		t.Fatal(err)
	} */
}

package pgs

import (
	"os"
	"strings"
	"testing"
	"time"

	sst "github.com/picosh/pico/pkg/pobj/storage"
	"github.com/picosh/pico/pkg/send/utils"
)

func TestGenerateDirectoryHTML(t *testing.T) {
	fixtures := []struct {
		Name     string
		Path     string
		Entries  []os.FileInfo
		Contains []string
	}{
		{
			Name:    "empty-directory",
			Path:    "/",
			Entries: []os.FileInfo{},
			Contains: []string{
				"<title>Index of /</title>",
				"Index of /",
			},
		},
		{
			Name: "single-file",
			Path: "/",
			Entries: []os.FileInfo{
				&utils.VirtualFile{FName: "hello.txt", FSize: 1024, FIsDir: false, FModTime: time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)},
			},
			Contains: []string{
				"<title>Index of /</title>",
				`href="hello.txt"`,
				"hello.txt",
				"1.0 KB",
			},
		},
		{
			Name: "single-folder",
			Path: "/",
			Entries: []os.FileInfo{
				&utils.VirtualFile{FName: "docs", FSize: 0, FIsDir: true, FModTime: time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)},
			},
			Contains: []string{
				`href="docs/"`,
				"docs/",
			},
		},
		{
			Name: "mixed-entries",
			Path: "/assets/",
			Entries: []os.FileInfo{
				&utils.VirtualFile{FName: "images", FSize: 0, FIsDir: true, FModTime: time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)},
				&utils.VirtualFile{FName: "style.css", FSize: 2048, FIsDir: false, FModTime: time.Date(2025, 1, 14, 8, 0, 0, 0, time.UTC)},
				&utils.VirtualFile{FName: "app.js", FSize: 512, FIsDir: false, FModTime: time.Date(2025, 1, 13, 12, 0, 0, 0, time.UTC)},
			},
			Contains: []string{
				"<title>Index of /assets/</title>",
				`href="images/"`,
				`href="style.css"`,
				`href="app.js"`,
				"images/",
				"2.0 KB",
			},
		},
		{
			Name: "subdirectory-with-parent-link",
			Path: "/docs/api/",
			Entries: []os.FileInfo{
				&utils.VirtualFile{FName: "readme.md", FSize: 256, FIsDir: false, FModTime: time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)},
			},
			Contains: []string{
				"<title>Index of /docs/api/</title>",
				`href="../"`,
				"../",
			},
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			html := generateDirectoryHTML(fixture.Path, fixture.Entries)

			for _, expected := range fixture.Contains {
				if !strings.Contains(html, expected) {
					t.Errorf("expected HTML to contain %q, got:\n%s", expected, html)
				}
			}
		})
	}
}

func TestSortEntries(t *testing.T) {
	entries := []os.FileInfo{
		&utils.VirtualFile{FName: "zebra.txt", FIsDir: false},
		&utils.VirtualFile{FName: "alpha", FIsDir: true},
		&utils.VirtualFile{FName: "beta.md", FIsDir: false},
		&utils.VirtualFile{FName: "zulu", FIsDir: true},
		&utils.VirtualFile{FName: "apple.js", FIsDir: false},
	}

	sortEntries(entries)

	expected := []string{"alpha", "zulu", "apple.js", "beta.md", "zebra.txt"}
	for i, entry := range entries {
		if entry.Name() != expected[i] {
			t.Errorf("position %d: expected %q, got %q", i, expected[i], entry.Name())
		}
	}
}

func TestShouldGenerateListing(t *testing.T) {
	fixtures := []struct {
		Name     string
		Path     string
		Storage  map[string]map[string]string
		Expected bool
	}{
		{
			Name: "directory-with-index-html",
			Path: "/docs/",
			Storage: map[string]map[string]string{
				"testbucket": {
					"/project/docs/index.html": "<html>hello</html>",
				},
			},
			Expected: false,
		},
		{
			Name: "directory-without-index-html",
			Path: "/docs/",
			Storage: map[string]map[string]string{
				"testbucket": {
					"/project/docs/readme.md": "# Readme",
					"/project/docs/guide.md":  "# Guide",
				},
			},
			Expected: true,
		},
		{
			Name: "empty-directory",
			Path: "/empty/",
			Storage: map[string]map[string]string{
				"testbucket": {
					"/project/other/file.txt": "content",
				},
			},
			Expected: false,
		},
		{
			Name: "root-directory-without-index",
			Path: "/",
			Storage: map[string]map[string]string{
				"testbucket": {
					"/project/style.css": "body {}",
					"/project/app.js":    "console.log('hi')",
				},
			},
			Expected: true,
		},
		{
			Name: "root-directory-with-index",
			Path: "/",
			Storage: map[string]map[string]string{
				"testbucket": {
					"/project/index.html": "<html>home</html>",
				},
			},
			Expected: false,
		},
		{
			Name: "nested-directory-without-index",
			Path: "/assets/images/",
			Storage: map[string]map[string]string{
				"testbucket": {
					"/project/assets/images/logo.png":   "png data",
					"/project/assets/images/banner.jpg": "jpg data",
				},
			},
			Expected: true,
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			st, _ := sst.NewStorageMemory(fixture.Storage)
			bucket := sst.Bucket{Name: "testbucket", Path: "testbucket"}

			result := shouldGenerateListing(st, bucket, "project", fixture.Path)

			if result != fixture.Expected {
				t.Errorf("shouldGenerateListing(%q) = %v, want %v", fixture.Path, result, fixture.Expected)
			}
		})
	}
}

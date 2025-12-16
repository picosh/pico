package pgs

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"os"
	"sort"
	"strings"

	sst "github.com/picosh/pico/pkg/pobj/storage"
)

//go:embed html/*
var dirListingFS embed.FS

var dirListingTmpl = template.Must(
	template.New("base").ParseFS(
		dirListingFS,
		"html/base.layout.tmpl",
		"html/marketing-footer.partial.tmpl",
		"html/directory_listing.page.tmpl",
	),
)

type dirEntryDisplay struct {
	Href    string
	Display string
	Size    string
	ModTime string
}

type DirectoryListingData struct {
	Path       string
	ShowParent bool
	Entries    []dirEntryDisplay
}

func formatFileSize(size int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case size >= GB:
		return fmt.Sprintf("%.1f GB", float64(size)/float64(GB))
	case size >= MB:
		return fmt.Sprintf("%.1f MB", float64(size)/float64(MB))
	case size >= KB:
		return fmt.Sprintf("%.1f KB", float64(size)/float64(KB))
	default:
		return fmt.Sprintf("%d B", size)
	}
}

func sortEntries(entries []os.FileInfo) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})
}

func toDisplayEntries(entries []os.FileInfo) []dirEntryDisplay {
	sortEntries(entries)
	displayEntries := make([]dirEntryDisplay, 0, len(entries))

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		display := dirEntryDisplay{
			Href:    entry.Name(),
			Display: entry.Name(),
			Size:    formatFileSize(entry.Size()),
			ModTime: entry.ModTime().Format("2006-01-02 15:04"),
		}

		if entry.IsDir() {
			display.Href += "/"
			display.Display += "/"
			display.Size = "-"
		}

		displayEntries = append(displayEntries, display)
	}

	return displayEntries
}

func shouldGenerateListing(st sst.ObjectStorage, bucket sst.Bucket, projectDir string, path string) bool {
	dirPath := projectDir + path
	if path == "/" {
		dirPath = projectDir + "/"
	}

	entries, err := st.ListObjects(bucket, dirPath, false)
	if err != nil || len(entries) == 0 {
		return false
	}

	indexPath := dirPath + "index.html"
	obj, _, err := st.GetObject(bucket, indexPath)
	if err != nil {
		return true
	}
	_ = obj.Close()
	return false
}

func generateDirectoryHTML(path string, entries []os.FileInfo) string {
	data := DirectoryListingData{
		Path:       path,
		ShowParent: path != "/",
		Entries:    toDisplayEntries(entries),
	}

	var buf bytes.Buffer
	if err := dirListingTmpl.Execute(&buf, data); err != nil {
		return fmt.Sprintf("Error rendering directory listing: %s", err)
	}

	return buf.String()
}

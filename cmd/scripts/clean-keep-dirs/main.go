// clean-keep-dirs removes the ._pico_keep_dir markers pgs used to write to
// keep empty directories. Filesystem storage keeps real directories now, so
// removing a marker leaves its directory in place.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/picosh/pico/pkg/storage"
)

const keepDirName = "._pico_keep_dir"

func main() {
	deleteFlag := flag.Bool("delete", false, "delete the markers found")
	flag.Parse()

	logger := slog.Default()

	adapter := storage.GetStorageTypeFromEnv()
	if adapter != "fs" {
		logger.Error("markers only exist on filesystem storage", "adapter", adapter)
		os.Exit(1)
	}
	st, err := storage.NewStorage(logger, adapter)
	if err != nil {
		logger.Error("failed to create storage", "err", err)
		os.Exit(1)
	}
	root := st.(*storage.StorageFS).Dir

	found, deleted, failed := 0, 0, 0
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			logger.Error("failed to read", "path", p, "err", err)
			failed++
			return nil
		}
		if d.IsDir() || d.Name() != keepDirName {
			return nil
		}
		found++
		if !*deleteFlag {
			fmt.Println(p)
			return nil
		}
		if err := os.Remove(p); err != nil {
			logger.Error("failed to delete marker", "path", p, "err", err)
			failed++
			return nil
		}
		deleted++
		return nil
	})
	if err != nil {
		logger.Error("failed to walk storage", "root", root, "err", err)
		os.Exit(1)
	}

	if !*deleteFlag {
		fmt.Printf("\nFound %d marker(s) under %s. Use --delete to remove them.\n", found, root)
		return
	}
	fmt.Printf("\nDeleted %d marker(s) under %s, %d failed\n", deleted, root, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

package shared

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/picosh/pico/pkg/send/utils"
)

func GetImgsBucketName(userID string) string {
	return userID
}

func GetAssetBucketName(userID string) string {
	return fmt.Sprintf("static-%s", userID)
}

func GetProjectName(entry *utils.FileEntry) string {
	if entry.Mode.IsDir() && strings.Count(entry.Filepath, string(os.PathSeparator)) == 0 {
		return entry.Filepath
	}

	dir := filepath.Dir(entry.Filepath)
	list := strings.Split(dir, string(os.PathSeparator))
	return list[1]
}

func GetAssetFileName(entry *utils.FileEntry) string {
	return entry.Filepath
}

package shared

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/picosh/send/send/utils"
)

func GetImgsBucketName(userID string) string {
	return userID
}

func GetAssetBucketName(userID string) string {
	return fmt.Sprintf("static-%s", userID)
}

func GetProjectName(entry *utils.FileEntry) string {
	dir := filepath.Dir(entry.Filepath)
	list := strings.Split(dir, string(os.PathSeparator))
	return list[1]
}

func GetAssetFileName(entry *utils.FileEntry) string {
	return entry.Filepath
}

package shared

import (
	"fmt"
	"path"
)

func GetAssetBucketName(userID string) string {
	return fmt.Sprintf("static-%s", userID)
}

func GetAssetFileName(fpath string, fname string) string {
	return path.Join(fpath, fname)
}

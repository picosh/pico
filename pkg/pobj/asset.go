package pobj

import (
	"fmt"

	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/utils"
)

type AssetNames interface {
	BucketName(sesh *pssh.SSHServerConnSession) (string, error)
	ObjectName(sesh *pssh.SSHServerConnSession, entry *utils.FileEntry) (string, error)
	PrintObjectName(sesh *pssh.SSHServerConnSession, entry *utils.FileEntry, bucketName string) (string, error)
}

type AssetNamesBasic struct{}

var _ AssetNames = &AssetNamesBasic{}
var _ AssetNames = (*AssetNamesBasic)(nil)

func (an *AssetNamesBasic) BucketName(sesh *pssh.SSHServerConnSession) (string, error) {
	return sesh.User(), nil
}
func (an *AssetNamesBasic) ObjectName(sesh *pssh.SSHServerConnSession, entry *utils.FileEntry) (string, error) {
	return entry.Filepath, nil
}
func (an *AssetNamesBasic) PrintObjectName(sesh *pssh.SSHServerConnSession, entry *utils.FileEntry, bucketName string) (string, error) {
	objectName, err := an.ObjectName(sesh, entry)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%s", bucketName, objectName), nil
}

type AssetNamesForceBucket struct {
	*AssetNamesBasic
	Name string
}

var _ AssetNames = &AssetNamesForceBucket{}
var _ AssetNames = (*AssetNamesForceBucket)(nil)

func (an *AssetNamesForceBucket) BucketName(sesh *pssh.SSHServerConnSession) (string, error) {
	return an.Name, nil
}

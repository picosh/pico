package config

import "log/slog"

type ConfigURL interface {
	BlogURL(username string) string
	PostURL(username string, filename string) string
}

type ConfigCms struct {
	Domain        string
	Port          string
	Email         string
	Protocol      string
	DbURL         string
	StorageDir    string
	MinioURL      string
	MinioUser     string
	MinioPass     string
	Description   string
	IntroText     string
	Space         string
	AllowedExt    []string
	HiddenPosts   []string
	Logger        *slog.Logger
	AllowRegister bool
	MaxSize       uint64
	MaxAssetSize  int64
}

func NewConfigCms() *ConfigCms {
	return &ConfigCms{}
}

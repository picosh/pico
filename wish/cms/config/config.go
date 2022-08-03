package config

import (
	"go.uber.org/zap"
)

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
	Description   string
	IntroText     string
	Space         string
	AllowedExt    []string
	HiddenPosts   []string
	Logger        *zap.SugaredLogger
	AllowRegister bool
}

func NewConfigCms() *ConfigCms {
	return &ConfigCms{}
}

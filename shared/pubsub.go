package shared

import (
	"github.com/picosh/pubsub"
	"github.com/picosh/utils"
)

func NewPicoPipeClient() *pubsub.RemoteClientInfo {
	return &pubsub.RemoteClientInfo{
		RemoteHost:     utils.GetEnv("PICO_PIPE_ENDPOINT", "pipe.pico.sh:22"),
		KeyLocation:    utils.GetEnv("PICO_PIPE_KEY", "ssh_data/term_info_ed25519"),
		KeyPassphrase:  utils.GetEnv("PICO_PIPE_PASSPHRASE", ""),
		RemoteHostname: utils.GetEnv("PICO_PIPE_REMOTE_HOST", "pipe.pico.sh"),
		RemoteUser:     utils.GetEnv("PICO_PIPE_USER", "pico"),
	}
}

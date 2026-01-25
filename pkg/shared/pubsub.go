package shared

import (
	"github.com/picosh/utils/pipe"
)

func NewPicoPipeClient() *pipe.SSHClientInfo {
	return &pipe.SSHClientInfo{
		RemoteHost:          GetEnv("PICO_PIPE_ENDPOINT", "pipe.pico.sh:22"),
		KeyLocation:         GetEnv("PICO_PIPE_KEY", "ssh_data/term_info_ed25519"),
		CertificateLocation: GetEnv("PICO_PIPE_KEY_CERT", ""),
		KeyPassphrase:       GetEnv("PICO_PIPE_PASSPHRASE", ""),
		RemoteHostname:      GetEnv("PICO_PIPE_REMOTE_HOST", "pipe.pico.sh"),
		RemoteUser:          GetEnv("PICO_PIPE_USER", "pico"),
	}
}

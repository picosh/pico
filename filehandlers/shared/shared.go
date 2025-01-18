package fileshared

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/picosh/pico/shared"
	pipeUtil "github.com/picosh/utils/pipe"
)

type FileUploaded struct {
	UserID      string `json:"user_id"`
	Action      string `json:"action"`
	Filename    string `json:"filename"`
	Service     string `json:"service"`
	ProjectName string `json:"project_name"`
}

func CreatePubUploadDrain(ctx context.Context, logger *slog.Logger) *pipeUtil.ReconnectReadWriteCloser {
	info := shared.NewPicoPipeClient()
	send := pipeUtil.NewReconnectReadWriteCloser(
		ctx,
		logger,
		info,
		"pub to upload-drain",
		"pub upload-drain -b=false",
		100,
		-1,
	)
	return send
}

func WriteUploadDrain(drain *pipeUtil.ReconnectReadWriteCloser, upload *FileUploaded) error {
	jso, err := json.Marshal(upload)
	if err != nil {
		return err
	}

	jso = append(jso, '\n')
	_, err = drain.Write(jso)
	return err
}

func CreateSubUploadDrain(ctx context.Context, logger *slog.Logger) *pipeUtil.ReconnectReadWriteCloser {
	info := shared.NewPicoPipeClient()
	send := pipeUtil.NewReconnectReadWriteCloser(
		ctx,
		logger,
		info,
		"sub to upload-drain",
		"sub upload-drain -k",
		100,
		-1,
	)
	return send
}

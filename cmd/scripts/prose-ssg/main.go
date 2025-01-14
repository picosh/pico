package main

import (
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/prose"
	"github.com/picosh/pico/shared/storage"
)

func bail(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	cfg := prose.NewConfigSite()
	picoDb := postgres.NewDB(cfg.DbURL, cfg.Logger)
	st, err := storage.NewStorageFS(cfg.Logger, cfg.StorageDir)
	bail(err)
	ssg := &prose.SSG{
		Cfg:       cfg,
		DB:        picoDb,
		Storage:   st,
		Logger:    cfg.Logger,
		TmplDir:   "./prose/html",
		StaticDir: "./prose/public",
	}
	bail(ssg.Prose())
}

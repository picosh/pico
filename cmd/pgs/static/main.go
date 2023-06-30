package main

import (
	"flag"

	"github.com/picosh/pico/pgs"
)

func main() {
	out := flag.String("out", "./public", "output folder for static assets")
	flag.Parse()
	cfg := pgs.NewConfigSite()
	err := pgs.GenStaticSite(*out, cfg)
	if err != nil {
		panic(err)
	}
}

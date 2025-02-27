package main

import (
	"fmt"

	"github.com/mmcdole/gofeed"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/feeds"
)

func main() {
	cfg := feeds.NewConfigSite("feeds-fetch")
	dbh := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer dbh.Close()
	fetcher := feeds.NewFetcher(dbh, cfg)
	fp := gofeed.NewParser()
	feed, err := fetcher.ParseURL(fp, "https://old.reddit.com/r/Watchexchange/.rss")
	if err != nil {
		panic(err)
	}
	fmt.Println(feed)
}

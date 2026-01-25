package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/picosh/pico/pkg/db/postgres"
	"github.com/picosh/pico/pkg/shared/router"
)

func main() {
	logger := slog.Default()
	DbURL := os.Getenv("DATABASE_URL")
	dbpool := postgres.NewDB(DbURL, logger)
	batchSize := 100_000
	offset := 0

	var totalRows int
	err := dbpool.Db.QueryRow("SELECT count(id) FROM analytics_visits").Scan(&totalRows)
	if err != nil {
		panic(err)
	}

	fmt.Println("TOTAL ROWS", totalRows)

	for {
		fmt.Println("===")
		fmt.Println("offset", offset)
		fmt.Println("===")
		rows, err := dbpool.Db.Query("SELECT id, host, referer FROM analytics_visits ORDER BY created_at DESC LIMIT $1 OFFSET $2", batchSize, offset)
		if err != nil {
			panic(err)
		}

		// Process the rows
		for rows.Next() {
			var id, origHost, origRef string
			err := rows.Scan(
				&id,
				&origHost,
				&origRef,
			)
			if err != nil {
				panic(err)
			}

			update := false

			host, err := router.CleanHost(origHost)
			if err != nil {
				fmt.Println(err)
			}

			if origHost != host {
				update = true
				fmt.Printf(
					"HOST %s->%s\n",
					origHost, host,
				)
			}

			ref, err := router.CleanReferer(origRef)
			if err != nil {
				fmt.Println(err)
			}

			if origRef != ref {
				update = true
				fmt.Printf(
					"REF %s->%s\n",
					origRef, ref,
				)
			}

			if update {
				fmt.Printf("Updating visit ID:%s\n", id)
				_, err := dbpool.Db.Exec(
					"UPDATE analytics_visits SET host=$1, referer=$2 WHERE id=$3",
					host,
					ref,
					id,
				)
				if err != nil {
					panic(err)
				}
			}
		}

		if rows.Err() != nil {
			panic(rows.Err())
		}

		offset += batchSize
		if offset >= totalRows {
			break
		}
	}
}

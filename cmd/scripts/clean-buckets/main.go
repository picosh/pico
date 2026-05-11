package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/picosh/pico/pkg/db/postgres"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/storage"
)

func main() {
	deleteFlag := flag.Bool("delete", false, "delete orphaned bucket folders")
	flag.Parse()

	logger := slog.Default()

	dbURL := shared.GetEnv("DATABASE_URL", "")
	if dbURL == "" {
		logger.Error("DATABASE_URL is required")
		os.Exit(1)
	}

	dbpool := postgres.NewDB(dbURL, logger)
	defer func() { _ = dbpool.Close() }()

	st, err := storage.NewStorage(logger, "fs")
	if err != nil {
		logger.Error("failed to create storage", "err", err)
		os.Exit(1)
	}

	// Collect all valid user IDs from the database
	logger.Info("fetching all users from database")
	users, err := dbpool.FindUsers()
	if err != nil {
		logger.Error("failed to fetch users", "err", err)
		os.Exit(1)
	}

	validUserIDs := make(map[string]struct{}, len(users))
	for _, user := range users {
		validUserIDs[user.ID] = struct{}{}
		logger.Info("found user", "id", user.ID, "name", user.Name)
	}
	logger.Info("total users", "count", len(validUserIDs))

	// List all buckets in the storage directory
	logger.Info("listing all buckets in storage")
	buckets, err := st.ListBuckets()
	if err != nil {
		logger.Error("failed to list buckets", "err", err)
		os.Exit(1)
	}
	logger.Info("total buckets", "count", len(buckets))

	// Find orphaned buckets (no associated user)
	orphaned := []string{}
	for _, bucket := range buckets {
		// Buckets are either the user ID directly (e.g., images)
		// or prefixed with "static-" (e.g., static-{userID} for assets)
		userID := bucket
		if strings.HasPrefix(bucket, "static-") {
			userID = strings.TrimPrefix(bucket, "static-")
		}

		if _, exists := validUserIDs[userID]; !exists {
			orphaned = append(orphaned, bucket)
		}
	}

	if len(orphaned) == 0 {
		logger.Info("no orphaned buckets found")
		return
	}

	logger.Info("orphaned buckets found", "count", len(orphaned))
	for _, bucket := range orphaned {
		fmt.Printf("  - %s\n", bucket)
	}

	if !*deleteFlag {
		fmt.Printf("\nFound %d orphaned bucket(s). Use --delete to remove them.\n", len(orphaned))
		return
	}

	// Delete orphaned buckets
	logger.Info("deleting orphaned buckets")
	deleted := 0
	failed := 0
	for _, bucket := range orphaned {
		b, err := st.GetBucket(bucket)
		if err != nil {
			logger.Error("failed to get bucket", "bucket", bucket, "err", err)
			failed++
			continue
		}

		err = st.DeleteBucket(b)
		if err != nil {
			logger.Error("failed to delete bucket", "bucket", bucket, "err", err)
			failed++
			continue
		}

		logger.Info("deleted bucket", "bucket", bucket)
		deleted++
	}

	fmt.Printf("\nDeleted %d bucket(s), %d failed\n", deleted, failed)
}

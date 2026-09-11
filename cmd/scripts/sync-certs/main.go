package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/picosh/pico/pkg/shared"
)

// StorageData matches caddy-storage-redis serialization schema.
type StorageData struct {
	Value       []byte    `json:"value"`
	Modified    time.Time `json:"modified"`
	Size        int64     `json:"size"`
	Compression int       `json:"compression"`
	Encryption  int       `json:"encryption"`
}

// ValkeyClient is a lightweight RESP client using only standard library.
type ValkeyClient struct {
	conn net.Conn
	br   *bufio.Reader
	bw   *bufio.Writer
}

func NewValkeyClient(addr, password string, db int, timeout time.Duration) (*ValkeyClient, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("connect to valkey (%s): %w", addr, err)
	}

	client := &ValkeyClient{
		conn: conn,
		br:   bufio.NewReader(conn),
		bw:   bufio.NewWriter(conn),
	}

	if password != "" {
		if _, err := client.Do("AUTH", password); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("authentication failed: %w", err)
		}
	}

	if db > 0 {
		if _, err := client.Do("SELECT", strconv.Itoa(db)); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("select db %d failed: %w", db, err)
		}
	}

	return client, nil
}

func (c *ValkeyClient) Close() error {
	return c.conn.Close()
}

func (c *ValkeyClient) Do(args ...string) (any, error) {
	if err := c.conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return nil, err
	}

	if _, err := fmt.Fprintf(c.bw, "*%d\r\n", len(args)); err != nil {
		return nil, err
	}
	for _, arg := range args {
		if _, err := fmt.Fprintf(c.bw, "$%d\r\n%s\r\n", len(arg), arg); err != nil {
			return nil, err
		}
	}
	if err := c.bw.Flush(); err != nil {
		return nil, err
	}

	return c.readReply()
}

func (c *ValkeyClient) readReply() (any, error) {
	line, err := c.br.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 3 {
		return nil, fmt.Errorf("malformed RESP reply: %q", line)
	}

	prefix := line[0]
	payload := strings.TrimRight(line[1:], "\r\n")

	switch prefix {
	case '+': // Simple string
		return payload, nil
	case '-': // Error
		return nil, errors.New(payload)
	case ':': // Integer
		return strconv.ParseInt(payload, 10, 64)
	case '$': // Bulk string
		length, err := strconv.Atoi(payload)
		if err != nil {
			return nil, err
		}
		if length == -1 {
			return nil, nil // Nil
		}
		buf := make([]byte, length+2)
		if _, err := io.ReadFull(c.br, buf); err != nil {
			return nil, err
		}
		return string(buf[:length]), nil
	case '*': // Array
		count, err := strconv.Atoi(payload)
		if err != nil {
			return nil, err
		}
		if count == -1 {
			return nil, nil
		}
		items := make([]any, count)
		for i := 0; i < count; i++ {
			items[i], err = c.readReply()
			if err != nil {
				return nil, err
			}
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unknown RESP prefix: %c", prefix)
	}
}

func (c *ValkeyClient) Exists(key string) (bool, error) {
	res, err := c.Do("EXISTS", key)
	if err != nil {
		return false, err
	}
	if n, ok := res.(int64); ok {
		return n > 0, nil
	}
	return false, nil
}

func (c *ValkeyClient) Set(key, value string) error {
	res, err := c.Do("SET", key, value)
	if err != nil {
		return err
	}
	if s, ok := res.(string); ok && s == "OK" {
		return nil
	}
	return fmt.Errorf("unexpected set response: %v", res)
}

func (c *ValkeyClient) ZAdd(key string, score float64, member string) error {
	_, err := c.Do("ZADD", key, strconv.FormatFloat(score, 'f', -1, 64), member)
	return err
}

// splitDirectoryKey reproduces caddy-storage-redis's directory hierarchy split.
func splitDirectoryKey(key string, baseIsDir bool) (string, string) {
	dir := path.Dir(key)
	base := path.Base(key)
	if baseIsDir {
		base += "/"
	}
	return dir, base
}

// storeDirectoryRecord recursively adds records into Redis Sorted Sets matching caddy-storage-redis.
func storeDirectoryRecord(client *ValkeyClient, key string, score float64, baseIsDir bool) error {
	dir, base := splitDirectoryKey(key, baseIsDir)
	if dir == "." {
		return nil
	}

	if err := client.ZAdd(dir, score, base); err != nil {
		return fmt.Errorf("unable to add %s to set %s: %w", base, dir, err)
	}

	return storeDirectoryRecord(client, dir, score, true)
}

type syncStats struct {
	found   int
	synced  int
	skipped int
	failed  int
}

// findCaddyRoots detects potential Caddy storage roots in given path.
func findCaddyRoots(targetDir string) []string {
	var roots []string

	// Check if targetDir is itself a Caddy storage root
	if isCaddyStorageRoot(targetDir) {
		return []string{targetDir}
	}

	// Check immediate subdirectories (e.g. `data/caddy` or service dirs)
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return nil
	}

	for _, e := range entries {
		if !e.IsDir() || e.Name() == "archive" {
			continue
		}
		sub := filepath.Join(targetDir, e.Name())
		if isCaddyStorageRoot(sub) {
			roots = append(roots, sub)
			continue
		}

		// Also check nested `.../data/caddy` commonly used in compose mounts
		nested := filepath.Join(sub, "data", "caddy")
		if isCaddyStorageRoot(nested) {
			roots = append(roots, nested)
			continue
		}
		nestedCaddy := filepath.Join(sub, "caddy")
		if isCaddyStorageRoot(nestedCaddy) {
			roots = append(roots, nestedCaddy)
		}
	}

	return roots
}

func isCaddyStorageRoot(dir string) bool {
	certs := filepath.Join(dir, "certificates")
	acme := filepath.Join(dir, "acme")
	if fi, err := os.Stat(certs); err == nil && fi.IsDir() {
		return true
	}
	if fi, err := os.Stat(acme); err == nil && fi.IsDir() {
		return true
	}
	return false
}

func syncFile(
	client *ValkeyClient,
	root string,
	filePath string,
	keyPrefix string,
	dryRun bool,
	overwrite bool,
	logger *slog.Logger,
) (synced bool, skipped bool, err error) {
	rel, err := filepath.Rel(root, filePath)
	if err != nil {
		return false, false, err
	}

	// Normalize to slash path
	caddyKey := filepath.ToSlash(rel)

	// Only sync active Caddy storage assets: certificates, acme accounts, and ocsp staples
	if !strings.HasPrefix(caddyKey, "certificates/") &&
		!strings.HasPrefix(caddyKey, "acme/") &&
		!strings.HasPrefix(caddyKey, "ocsp/") {
		return false, true, nil
	}

	// Skip temporary locks, write tests, or hidden files
	baseName := filepath.Base(caddyKey)
	if strings.Contains(caddyKey, "/locks/") || strings.HasPrefix(baseName, ".") || strings.HasPrefix(baseName, "rw_test_") {
		return false, true, nil
	}

	prefixedKey := path.Join(keyPrefix, caddyKey)

	fi, err := os.Stat(filePath)
	if err != nil {
		return false, false, err
	}

	if !overwrite && client != nil {
		exists, err := client.Exists(prefixedKey)
		if err != nil {
			return false, false, fmt.Errorf("check exists %s: %w", prefixedKey, err)
		}
		if exists {
			logger.Debug("key already exists in valkey, skipping", "key", prefixedKey)
			return false, true, nil
		}
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return false, false, err
	}

	sd := StorageData{
		Value:       content,
		Modified:    fi.ModTime().UTC(),
		Size:        int64(len(content)),
		Compression: 0,
		Encryption:  0,
	}

	jsonBytes, err := json.Marshal(sd)
	if err != nil {
		return false, false, fmt.Errorf("marshal storage data for %s: %w", caddyKey, err)
	}

	if dryRun {
		logger.Info("[dry-run] would sync", "key", prefixedKey, "size", len(content), "modTime", sd.Modified)
		return true, false, nil
	}

	score := float64(sd.Modified.Unix())
	if err := storeDirectoryRecord(client, prefixedKey, score, false); err != nil {
		return false, false, fmt.Errorf("index directory for %s: %w", prefixedKey, err)
	}

	if err := client.Set(prefixedKey, string(jsonBytes)); err != nil {
		return false, false, fmt.Errorf("set key %s: %w", prefixedKey, err)
	}

	logger.Info("synced", "key", prefixedKey, "size", len(content))
	return true, false, nil
}

func main() {
	defaultAddr := shared.GetEnv("VALKEY_ADDR", shared.GetEnv("REDIS_ADDR", ""))
	defaultPass := shared.GetEnv("VALKEY_PASSWORD", shared.GetEnv("REDIS_PASSWORD", ""))
	defaultPrefix := shared.GetEnv("KEY_PREFIX", "caddy")

	addrFlag := flag.String("addr", defaultAddr, "Valkey/Redis server address (host:port)")
	passFlag := flag.String("password", defaultPass, "Valkey/Redis password")
	dbFlag := flag.Int("db", 0, "Valkey database index")
	prefixFlag := flag.String("prefix", defaultPrefix, "Caddy storage key prefix")
	dirFlag := flag.String("dir", "", "Directory containing Caddy storage (required)")
	dryRunFlag := flag.Bool("dry-run", false, "Preview keys without uploading to Valkey")
	overwriteFlag := flag.Bool("overwrite", false, "Overwrite keys even if they already exist in Valkey")
	verboseFlag := flag.Bool("verbose", false, "Enable verbose logging")
	flag.Parse()

	logLevel := slog.LevelInfo
	if *verboseFlag {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))

	dir := *dirFlag
	if dir == "" && flag.NArg() > 0 {
		dir = flag.Arg(0)
	}
	if dir == "" {
		logger.Error("directory is required (provide -dir <path> or path as argument)")
		os.Exit(1)
	}

	if *addrFlag == "" {
		logger.Error("address is required (provide -addr or set VALKEY_ADDR)")
		os.Exit(1)
	}

	if *passFlag == "" {
		logger.Error("password is required (provide -password or set VALKEY_PASSWORD)")
		os.Exit(1)
	}

	roots := findCaddyRoots(dir)
	if len(roots) == 0 {
		logger.Error("no Caddy storage directories found in path", "dir", dir)
		os.Exit(1)
	}

	logger.Info("discovered Caddy storage roots", "count", len(roots), "roots", roots)

	var client *ValkeyClient
	if !*dryRunFlag {
		var err error
		client, err = NewValkeyClient(*addrFlag, *passFlag, *dbFlag, 5*time.Second)
		if err != nil {
			logger.Error("failed to connect to Valkey", "err", err, "addr", *addrFlag)
			os.Exit(1)
		}
		defer func() { _ = client.Close() }()
		logger.Info("connected to Valkey successfully", "addr", *addrFlag, "db", *dbFlag)
	} else {
		logger.Info("running in DRY-RUN mode; no changes will be made to Valkey")
	}

	stats := syncStats{}

	for _, root := range roots {
		logger.Info("syncing Caddy root", "path", root)
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}

			stats.found++
			synced, skipped, err := syncFile(client, root, p, *prefixFlag, *dryRunFlag, *overwriteFlag, logger)
			if err != nil {
				logger.Error("failed to sync file", "path", p, "err", err)
				stats.failed++
				return nil
			}
			if synced {
				stats.synced++
			} else if skipped {
				stats.skipped++
			}
			return nil
		})
		if err != nil {
			logger.Error("error walking directory", "root", root, "err", err)
		}
	}

	fmt.Printf("\n--- Valkey TLS Migration Summary ---\n")
	fmt.Printf("Total files found:   %d\n", stats.found)
	fmt.Printf("Successfully synced: %d\n", stats.synced)
	fmt.Printf("Skipped (existing):  %d\n", stats.skipped)
	fmt.Printf("Failed:              %d\n", stats.failed)
	if *dryRunFlag {
		fmt.Printf("Mode:                DRY RUN (no keys written)\n")
	}
	fmt.Printf("------------------------------------\n")

	if stats.failed > 0 {
		os.Exit(1)
	}
}

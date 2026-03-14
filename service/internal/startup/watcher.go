// Package startup — background DB watcher.
package startup

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/521studios/pfsrd2-data-api/internal/s3"
)

const defaultWatchInterval = time.Hour

// StartDBWatcher starts a background goroutine that periodically checks S3
// for a new DB version and downloads it if the ETag changed.
//
// The check interval defaults to 1h but can be overridden via the
// WATCHER_INTERVAL environment variable (Go duration string, e.g. "30m").
func StartDBWatcher(ctx context.Context, cfg Config) {
	interval := watchInterval()
	log.Printf("[watcher] starting DB watcher (interval=%s)", interval)
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Printf("[watcher] shutting down")
				return
			case <-ticker.C:
				checkAndRefresh(ctx, cfg)
			}
		}
	}()
}

func checkAndRefresh(ctx context.Context, cfg Config) {
	now := time.Now().UTC().Format(time.RFC3339)
	status.LastChecked = now

	s3Etag, err := cfg.S3Client.HeadObject(ctx, s3.DBKey)
	if err != nil {
		log.Printf("[watcher] HEAD failed: %v", err)
		return
	}
	status.S3Etag = s3Etag

	local := readEtag()
	if local == s3Etag {
		log.Printf("[watcher] DB up to date (etag=%s)", s3Etag)
		status.UpToDate = true
		return
	}

	log.Printf("[watcher] DB changed (local=%s s3=%s), refreshing …", local, s3Etag)
	if err := downloadDB(ctx, cfg.S3Client); err != nil {
		log.Printf("[watcher] refresh failed: %v", err)
		return
	}
	writeEtag(s3Etag)
	status.LocalEtag = s3Etag
	status.UpToDate = true
	status.LastRefreshed = time.Now().UTC().Format(time.RFC3339)

	if err := openAndSet(); err != nil {
		log.Printf("[watcher] reopen failed after refresh: %v", err)
	} else {
		log.Printf("[watcher] DB refreshed (etag=%s)", s3Etag)
	}
}

func watchInterval() time.Duration {
	raw := os.Getenv("WATCHER_INTERVAL")
	if raw == "" {
		return defaultWatchInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		log.Printf("[watcher] invalid WATCHER_INTERVAL %q, using default 1h", raw)
		return defaultWatchInterval
	}
	return d
}

// Package startup handles cold-start DB download and ETag-based freshness checks.
package startup

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/521studios/pfsrd2-data-api/internal/db"
	"github.com/521studios/pfsrd2-data-api/internal/s3"
)

const (
	dbPath   = "/tmp/pfsrd2.db"
	etagPath = "/tmp/.db_etag"
)

// Config holds startup configuration sourced from environment variables.
type Config struct {
	BucketName    string
	ImageDomain   string
	WatchInterval string // e.g. "1h"
	S3Client      *s3.Client
}

// DBStatus is returned by Status() and by GET /db/status.
type DBStatus struct {
	LocalEtag     string `json:"local_etag"`
	S3Etag        string `json:"s3_etag"`
	UpToDate      bool   `json:"up_to_date"`
	LastChecked   string `json:"last_checked"`
	LastRefreshed string `json:"last_refreshed"`
}

var status DBStatus

// InitDB performs a cold-start DB download if the ETag has changed.
// Returns the opened *sql.DB (also sets db.Global).
func InitDB(ctx context.Context, cfg Config) error {
	s3Etag, err := cfg.S3Client.HeadObject(ctx, s3.DBKey)
	if err != nil {
		// Credentials/network failure at startup must not take the server
		// down when a previously downloaded DB exists (expired SSO in local
		// dev crash-looped the container). Serve stale and let the watcher
		// refresh once S3 is reachable again.
		if _, statErr := os.Stat(dbPath); statErr == nil {
			log.Printf("[startup] head db failed (%v), using stale local DB", err)
			return openAndSet()
		}
		return fmt.Errorf("head db: %w", err)
	}

	localEtag := readEtag()
	status.LocalEtag = localEtag
	status.S3Etag = s3Etag

	if localEtag != "" && localEtag == s3Etag {
		log.Printf("[startup] DB up to date (etag=%s), skipping download", localEtag)
		status.UpToDate = true
		return openAndSet()
	}

	log.Printf("[startup] DB etag changed (%s → %s), downloading …", localEtag, s3Etag)
	if err := downloadDB(ctx, cfg.S3Client); err != nil {
		// If download fails but we have a local copy, use it.
		if _, statErr := os.Stat(dbPath); statErr == nil {
			log.Printf("[startup] download failed (%v), using stale local DB", err)
			return openAndSet()
		}
		return fmt.Errorf("download db: %w", err)
	}

	writeEtag(s3Etag)
	status.LocalEtag = s3Etag
	status.UpToDate = true
	return openAndSet()
}

// downloadDB streams the DB from S3 to a temp file, then atomically renames.
func downloadDB(ctx context.Context, s3c *s3.Client) error {
	body, err := s3c.GetObject(ctx, s3.DBKey)
	if err != nil {
		return err
	}
	defer body.Close()

	tmp := dbPath + ".new"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	if _, err := io.Copy(f, body); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write tmp: %w", err)
	}
	f.Close()
	return os.Rename(tmp, dbPath)
}

func openAndSet() error {
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		return err
	}
	db.SetGlobal(sqlDB)
	return nil
}

func readEtag() string {
	b, err := os.ReadFile(etagPath)
	if err != nil {
		return ""
	}
	return string(b)
}

func writeEtag(etag string) {
	if err := os.WriteFile(etagPath, []byte(etag), 0644); err != nil {
		log.Printf("[startup] warn: could not write etag file: %v", err)
	}
}

// Status returns the current DB freshness status.
func Status() DBStatus {
	return status
}

// ForceRefresh re-downloads the DB unconditionally (for POST /db/refresh).
func ForceRefresh(ctx context.Context, cfg Config) error {
	log.Printf("[startup] force refresh requested")
	if err := downloadDB(ctx, cfg.S3Client); err != nil {
		return err
	}
	s3Etag, err := cfg.S3Client.HeadObject(ctx, s3.DBKey)
	if err != nil {
		log.Printf("[startup] warn: could not fetch ETag after refresh: %v", err)
	} else {
		writeEtag(s3Etag)
		status.LocalEtag = s3Etag
		status.S3Etag = s3Etag
		status.UpToDate = true
	}

	if err := openAndSet(); err != nil {
		return fmt.Errorf("reopen after refresh: %w", err)
	}
	log.Printf("[startup] force refresh done (etag=%s)", s3Etag)
	return nil
}

// DBPath returns the local DB file path (for tests and local dev).
func DBPath() string { return filepath.Clean(dbPath) }

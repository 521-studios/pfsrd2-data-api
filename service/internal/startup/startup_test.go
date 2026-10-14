package startup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var errUnreachable = errors.New("s3 unreachable")

// unreachableStore fails every call, so the watcher and ForceRefresh update
// the status without touching the DB files on disk.
type unreachableStore struct{}

func (unreachableStore) HeadObject(context.Context, string) (string, error) {
	return "", errUnreachable
}

func (unreachableStore) GetObject(context.Context, string) (io.ReadCloser, error) {
	return nil, errUnreachable
}

// Run with -race: the watcher writes the status while handlers read it and
// request refreshes.
func TestWatcherStatusAndForceRefreshConcurrently(t *testing.T) {
	t.Setenv("WATCHER_INTERVAL", "1ms")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	StartDBWatcher(ctx, Config{S3Client: unreachableStore{}})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = Status()
				if err := ForceRefresh(ctx); !errors.Is(err, errUnreachable) {
					t.Errorf("ForceRefresh err = %v, want %v", err, errUnreachable)
				}
			}
		}()
	}
	wg.Wait()

	deadline := time.Now().Add(time.Second)
	for Status().LastChecked == "" {
		if time.Now().After(deadline) {
			t.Fatal("watcher never published a status")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestForceRefreshReturnsWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ForceRefresh(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ForceRefresh err = %v, want context.Canceled", err)
	}
}

// fakeStore serves one object with a fixed ETag and counts downloads.
type fakeStore struct {
	etag      string
	body      string
	getErr    error
	downloads int
}

func (f *fakeStore) HeadObject(context.Context, string) (string, error) { return f.etag, nil }

func (f *fakeStore) GetObject(context.Context, string) (io.ReadCloser, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	f.downloads++
	return io.NopCloser(strings.NewReader(f.body)), nil
}

// useTempPaths points the DB and ETag files at a temp dir, optionally seeding
// a local DB with the given ETag ("" = no local copy).
func useTempPaths(t *testing.T, localEtag string) {
	t.Helper()
	dir := t.TempDir()
	savedDB, savedEtag := dbPath, etagPath
	dbPath, etagPath = filepath.Join(dir, "pfsrd2.db"), filepath.Join(dir, ".db_etag")
	t.Cleanup(func() { dbPath, etagPath = savedDB, savedEtag })
	if localEtag != "" {
		writeFile(t, dbPath, "old")
		writeFile(t, etagPath, localEtag)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInitDB(t *testing.T) {
	t.Run("up to date skips download", func(t *testing.T) {
		useTempPaths(t, "e1")
		store := &fakeStore{etag: "e1"}
		if err := InitDB(context.Background(), Config{S3Client: store}); err != nil {
			t.Fatal(err)
		}
		if store.downloads != 0 {
			t.Errorf("downloads = %d, want 0", store.downloads)
		}
		if got := Status(); !got.UpToDate || got.LocalEtag != "e1" || got.S3Etag != "e1" {
			t.Errorf("status = %+v", got)
		}
	})
	t.Run("changed etag downloads", func(t *testing.T) {
		useTempPaths(t, "")
		store := &fakeStore{etag: "e2", body: "new"}
		if err := InitDB(context.Background(), Config{S3Client: store}); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, dbPath); got != "new" {
			t.Errorf("db = %q, want new", got)
		}
		if got := readEtag(); got != "e2" {
			t.Errorf("etag file = %q, want e2", got)
		}
		if got := Status(); !got.UpToDate || got.LocalEtag != "e2" {
			t.Errorf("status = %+v", got)
		}
	})
	t.Run("head failure serves stale local copy", func(t *testing.T) {
		useTempPaths(t, "old-etag")
		if err := InitDB(context.Background(), Config{S3Client: unreachableStore{}}); err != nil {
			t.Fatal(err)
		}
		if got := Status(); got.UpToDate || got.LocalEtag != "old-etag" {
			t.Errorf("status = %+v", got)
		}
	})
}

func TestCheckAndRefresh(t *testing.T) {
	t.Run("unchanged etag", func(t *testing.T) {
		useTempPaths(t, "e1")
		store := &fakeStore{etag: "e1"}
		var st DBStatus
		checkAndRefresh(context.Background(), Config{S3Client: store}, &st)
		if store.downloads != 0 || !st.UpToDate || st.LastChecked == "" || st.LastRefreshed != "" {
			t.Errorf("downloads=%d status=%+v", store.downloads, st)
		}
	})
	t.Run("changed etag refreshes", func(t *testing.T) {
		useTempPaths(t, "e1")
		store := &fakeStore{etag: "e2", body: "new"}
		var st DBStatus
		checkAndRefresh(context.Background(), Config{S3Client: store}, &st)
		if got := readFile(t, dbPath); got != "new" {
			t.Errorf("db = %q, want new", got)
		}
		if !st.UpToDate || st.LocalEtag != "e2" || st.LastRefreshed == "" {
			t.Errorf("status = %+v", st)
		}
	})
	t.Run("download failure keeps old copy", func(t *testing.T) {
		useTempPaths(t, "e1")
		store := &fakeStore{etag: "e2", getErr: errUnreachable}
		st := DBStatus{LocalEtag: "e1"}
		checkAndRefresh(context.Background(), Config{S3Client: store}, &st)
		if got := readFile(t, dbPath); got != "old" {
			t.Errorf("db = %q, want old", got)
		}
		if st.UpToDate || st.LocalEtag != "e1" || st.S3Etag != "e2" || st.LastRefreshed != "" {
			t.Errorf("status = %+v", st)
		}
	})
}

func TestForceRefreshDownloadsUnconditionally(t *testing.T) {
	useTempPaths(t, "e1")
	store := &fakeStore{etag: "e1", body: "fresh"}
	var st DBStatus
	if err := forceRefresh(context.Background(), Config{S3Client: store}, &st); err != nil {
		t.Fatal(err)
	}
	if store.downloads != 1 || readFile(t, dbPath) != "fresh" {
		t.Errorf("downloads=%d db=%q", store.downloads, readFile(t, dbPath))
	}
	if !st.UpToDate || st.LocalEtag != "e1" {
		t.Errorf("status = %+v", st)
	}
}

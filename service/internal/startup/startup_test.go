package startup

import (
	"context"
	"errors"
	"io"
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

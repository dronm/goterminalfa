package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "requests.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestClaimCompleteAndReplay(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	claim, err := s.Claim(ctx, "kkt-1", "receipt-1", "fingerprint")
	if err != nil || !claim.Acquired || claim.Record.State != StateInProgress {
		t.Fatalf("claim = %+v, err = %v", claim, err)
	}
	if claim.Record.CreatedAt.IsZero() || claim.Record.CreatedAt.Location().String() != "UTC" {
		t.Fatalf("invalid created time: %v", claim.Record.CreatedAt)
	}
	duplicate, err := s.Claim(ctx, "kkt-1", "receipt-1", "fingerprint")
	if err != nil || duplicate.Acquired || duplicate.Record.State != StateInProgress {
		t.Fatalf("duplicate = %+v, err = %v", duplicate, err)
	}
	response := []byte("{\"ok\":true,\"data\":{\"documentNumber\":42}}\n")
	if err := s.Complete(ctx, "kkt-1", "receipt-1", 201, response); err != nil {
		t.Fatal(err)
	}
	replay, err := s.Claim(ctx, "kkt-1", "receipt-1", "fingerprint")
	if err != nil || replay.Acquired || replay.Record.State != StateCompleted {
		t.Fatalf("replay = %+v, err = %v", replay, err)
	}
	if replay.Record.HTTPStatus != 201 || !bytes.Equal(replay.Record.Response, response) {
		t.Fatalf("response not preserved: %+v", replay.Record)
	}
	if err := s.Complete(ctx, "kkt-1", "receipt-1", 200, []byte("overwritten")); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("second completion should fail: %v", err)
	}
	unchanged, err := s.Get(ctx, "kkt-1", "receipt-1")
	if err != nil || !bytes.Equal(unchanged.Response, response) {
		t.Fatalf("completed response changed: %+v, %v", unchanged, err)
	}
}

func TestConflictingParametersAndDeviceScope(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.Claim(ctx, "kkt-1", "same-id", "first-payload"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(ctx, "kkt-1", "same-id", "different-payload"); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict not detected: %v", err)
	}
	other, err := s.Claim(ctx, "kkt-2", "same-id", "different-payload")
	if err != nil || !other.Acquired {
		t.Fatalf("other device cannot claim same ID: %+v, %v", other, err)
	}
}

func TestConcurrentClaimsAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.sqlite")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	var acquired atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	start := make(chan struct{})
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			s := first
			if index%2 == 0 {
				s = second
			}
			claim, err := s.Claim(context.Background(), "kkt-1", "concurrent", "payload")
			if err != nil {
				errs <- err
				return
			}
			if claim.Acquired {
				acquired.Add(1)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if acquired.Load() != 1 {
		t.Fatalf("expected exactly one owner, got %d", acquired.Load())
	}
}

func TestRestartRecoversOnlyInterruptedRequests(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "requests.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, id := range []string{"done", "interrupted"} {
		if _, err := s.Claim(ctx, "kkt-1", id, id); err != nil {
			t.Fatal(err)
		}
	}
	response := []byte("{\"ok\":true}\n")
	if err := s.Complete(ctx, "kkt-1", "done", 200, response); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	done, err := reopened.Claim(ctx, "kkt-1", "done", "done")
	if err != nil || done.Acquired || done.Record.State != StateCompleted || !bytes.Equal(done.Record.Response, response) {
		t.Fatalf("completed result lost: %+v, %v", done, err)
	}
	unknown, err := reopened.Claim(ctx, "kkt-1", "interrupted", "interrupted")
	if err != nil || unknown.Acquired || unknown.Record.State != StateUnknown {
		t.Fatalf("interrupted command should be unknown: %+v, %v", unknown, err)
	}
	if err := reopened.Complete(ctx, "kkt-1", "interrupted", 200, response); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("unknown request should not be completed automatically: %v", err)
	}
	if err := reopened.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("database permissions: %v, %v", info, err)
		}
		info, err = os.Stat(filepath.Dir(path))
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("directory permissions: %v, %v", info, err)
		}
	}
}

func TestInvalidAndMissingRequests(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.Get(ctx, "missing", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing: %v", err)
	}
	if err := s.Complete(ctx, "missing", "missing", 200, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Complete missing: %v", err)
	}
	if err := s.MarkUnknown(ctx, "missing", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("MarkUnknown missing: %v", err)
	}
	if _, err := s.Claim(ctx, "", "id", "hash"); err == nil {
		t.Fatal("accepted empty device ID")
	}
	if _, err := s.Claim(ctx, "kkt", "", "hash"); err == nil {
		t.Fatal("accepted empty request ID")
	}
	if _, err := s.Claim(ctx, "kkt", "id", ""); err == nil {
		t.Fatal("accepted empty fingerprint")
	}
	if err := s.Complete(ctx, "kkt", "id", 100, nil); err == nil {
		t.Fatal("accepted non-final HTTP status")
	}
	if _, err := Open(":memory:"); err == nil {
		t.Fatal("accepted non-persistent store")
	}
}

func TestMarkUnknownBlocksReplayExecution(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.Claim(ctx, "kkt", "ambiguous", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkUnknown(ctx, "kkt", "ambiguous"); err != nil {
		t.Fatal(err)
	}
	claim, err := s.Claim(ctx, "kkt", "ambiguous", "hash")
	if err != nil || claim.Acquired || claim.Record.State != StateUnknown {
		t.Fatalf("unknown request reacquired: %+v, %v", claim, err)
	}
	if err := s.MarkUnknown(ctx, "kkt", "ambiguous"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("second transition should fail: %v", err)
	}
	if err := s.Complete(ctx, "kkt", "ambiguous", 200, nil); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("unknown request completed: %v", err)
	}
}

func TestCanonicalRequestSurvivesRestartAndConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	params := []byte(`{"amount":"150.00","items":[{"name":"item","quantity":"1"}]}`)
	claim, err := s.ClaimRequest(ctx, "kkt", "receipt", "original-hash", "createReceipt", params)
	if err != nil || !claim.Acquired || claim.Record.Command != "createReceipt" || !bytes.Equal(claim.Record.Params, params) {
		t.Fatalf("canonical request not stored atomically: %+v, %v", claim, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.ClaimRequest(ctx, "kkt", "receipt", "changed-hash", "otherCommand", []byte(`{"amount":"999.00"}`)); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting request accepted: %v", err)
	}
	record, err := reopened.Get(ctx, "kkt", "receipt")
	if err != nil || record.State != StateUnknown || record.Command != "createReceipt" || !bytes.Equal(record.Params, params) {
		t.Fatalf("original request changed after restart/conflict: %+v, %v", record, err)
	}
	duplicate, err := reopened.ClaimRequest(ctx, "kkt", "receipt", "original-hash", "createReceipt", params)
	if err != nil || duplicate.Acquired || duplicate.Record.State != StateUnknown {
		t.Fatalf("unknown canonical request reacquired: %+v, %v", duplicate, err)
	}
}

func TestCanceledClaimDoesNotAcquire(t *testing.T) {
	s := openTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Claim(ctx, "kkt", "canceled", "hash"); !errors.Is(err, context.Canceled) {
		t.Fatalf("claim with canceled context: %v", err)
	}
	if _, err := s.Get(context.Background(), "kkt", "canceled"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("canceled request persisted: %v", err)
	}
}

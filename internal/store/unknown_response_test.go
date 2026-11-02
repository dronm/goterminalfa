package store

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestUnknownResponsePersistsExactlyAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "requests.sqlite")
	journal, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	params := []byte(`{"items":[{"name":"A","quantity":"1"}]}`)
	claim, err := journal.ClaimRequest(ctx, "kkt-1", "receipt-1", "fingerprint", "createReceipt", params)
	if err != nil || !claim.Acquired {
		t.Fatalf("claim=%+v error=%v", claim, err)
	}
	body := []byte("{\n  \"status\":\"unknown\",\"data\":{\"failedStage\":\"finalizeReceipt\",\"cleanup\":{\"attempted\":false}},\"error\":{\"code\":\"outcome_unknown\"}\n}\n")
	want := append([]byte{}, body...)
	if err := journal.MarkUnknownWithResponse(ctx, "kkt-1", "receipt-1", body); err != nil {
		t.Fatal(err)
	}
	// The persisted bytes do not share the caller's mutable buffer.
	body[0] = '!'
	before, err := journal.Get(ctx, "kkt-1", "receipt-1")
	if err != nil {
		t.Fatal(err)
	}
	if before.State != StateUnknown || before.HTTPStatus != 0 || !bytes.Equal(before.Response, want) {
		t.Fatalf("unknown body/state not stored: %+v", before)
	}
	if err := journal.Close(); err != nil {
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
	record, err := reopened.Get(ctx, "kkt-1", "receipt-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.State != StateUnknown || record.HTTPStatus != 0 || !bytes.Equal(record.Response, want) || record.Command != "createReceipt" || !bytes.Equal(record.Params, params) {
		t.Fatalf("unknown response or original request changed after restart: %+v", record)
	}
	if !record.CreatedAt.Equal(claim.Record.CreatedAt) || record.UpdatedAt.Before(claim.Record.UpdatedAt) {
		t.Fatalf("unexpected persisted timestamps: %+v", record)
	}
	duplicate, err := reopened.ClaimRequest(ctx, "kkt-1", "receipt-1", "fingerprint", "createReceipt", params)
	if err != nil || duplicate.Acquired || duplicate.Record.State != StateUnknown || !bytes.Equal(duplicate.Record.Response, want) {
		t.Fatalf("unknown request re-acquired or lost its response: %+v, %v", duplicate, err)
	}
	if _, err := reopened.Claim(ctx, "kkt-1", "receipt-1", "changed"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected fingerprint conflict, got %v", err)
	}
	if err := reopened.Complete(ctx, "kkt-1", "receipt-1", 200, []byte("replacement")); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("unknown request was completed: %v", err)
	}
	if err := reopened.MarkUnknownWithResponse(ctx, "kkt-1", "receipt-1", []byte("replacement")); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("unknown response was overwritten: %v", err)
	}
	if err := reopened.MarkUnknown(ctx, "kkt-1", "receipt-1"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("generic transition erased the detailed unknown response: %v", err)
	}
	unchanged, err := reopened.Get(ctx, "kkt-1", "receipt-1")
	if err != nil || !bytes.Equal(unchanged.Response, want) || unchanged.HTTPStatus != 0 {
		t.Fatalf("forbidden state transition modified stored body: %+v, %v", unchanged, err)
	}
}

func TestUnknownResponseNilAndInvalidStates(t *testing.T) {
	journal := openTestStore(t)
	ctx := context.Background()
	if err := journal.MarkUnknownWithResponse(ctx, "kkt", "missing", []byte("body")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing request error=%v", err)
	}
	for _, id := range []string{"generic", "explicit-nil", "completed"} {
		if _, err := journal.Claim(ctx, "kkt", id, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := journal.MarkUnknown(ctx, "kkt", "generic"); err != nil {
		t.Fatal(err)
	}
	if err := journal.MarkUnknownWithResponse(ctx, "kkt", "explicit-nil", nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"generic", "explicit-nil"} {
		record, err := journal.Get(ctx, "kkt", id)
		if err != nil || record.State != StateUnknown || record.HTTPStatus != 0 || len(record.Response) != 0 {
			t.Fatalf("nil/generic unknown behavior changed: %+v, %v", record, err)
		}
	}
	want := []byte(`{"status":"completed"}`)
	if err := journal.Complete(ctx, "kkt", "completed", 200, want); err != nil {
		t.Fatal(err)
	}
	if err := journal.MarkUnknownWithResponse(ctx, "kkt", "completed", []byte("unknown")); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("completed request became unknown: %v", err)
	}
	record, err := journal.Get(ctx, "kkt", "completed")
	if err != nil || record.State != StateCompleted || record.HTTPStatus != 200 || !bytes.Equal(record.Response, want) {
		t.Fatalf("completed result changed: %+v, %v", record, err)
	}
}

func TestUnknownResponseTransitionHasOneWinner(t *testing.T) {
	journal := openTestStore(t)
	ctx := context.Background()
	if _, err := journal.Claim(ctx, "kkt", "racing", "hash"); err != nil {
		t.Fatal(err)
	}
	bodies := [][]byte{[]byte("first\x00body\n"), []byte("second body\n")}
	type outcome struct {
		index int
		err   error
	}
	results := make(chan outcome, len(bodies))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index, body := range bodies {
		wg.Add(1)
		go func(index int, body []byte) {
			defer wg.Done()
			<-start
			results <- outcome{index: index, err: journal.MarkUnknownWithResponse(ctx, "kkt", "racing", body)}
		}(index, body)
	}
	close(start)
	wg.Wait()
	close(results)
	winner := -1
	for result := range results {
		if result.err == nil {
			if winner >= 0 {
				t.Fatal("more than one unknown transition succeeded")
			}
			winner = result.index
		} else if !errors.Is(result.err, ErrInvalidState) {
			t.Fatal(result.err)
		}
	}
	if winner < 0 {
		t.Fatal("no transition succeeded")
	}
	record, err := journal.Get(ctx, "kkt", "racing")
	if err != nil || record.State != StateUnknown || record.HTTPStatus != 0 || !bytes.Equal(record.Response, bodies[winner]) {
		t.Fatalf("unknown state/body did not come from the same winning transition: %+v, %v", record, err)
	}
}

func TestCanceledUnknownResponseDoesNotChangeClaim(t *testing.T) {
	journal := openTestStore(t)
	if _, err := journal.Claim(context.Background(), "kkt", "cancel", "hash"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := journal.MarkUnknownWithResponse(ctx, "kkt", "cancel", []byte("body")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled update returned %v", err)
	}
	record, err := journal.Get(context.Background(), "kkt", "cancel")
	if err != nil || record.State != StateInProgress || record.HTTPStatus != 0 || len(record.Response) != 0 {
		t.Fatalf("canceled update changed request: %+v, %v", record, err)
	}
}

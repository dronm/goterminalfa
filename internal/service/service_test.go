package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	terminalfa "github.com/dronm/goterminalfa"
	"github.com/dronm/goterminalfa/internal/protocol"
	"github.com/dronm/goterminalfa/internal/store"
	"github.com/sirupsen/logrus"
)

type exchangeFunc func(context.Context, byte, []byte) ([]byte, error)

func (f exchangeFunc) Exchange(ctx context.Context, command byte, data []byte) ([]byte, error) {
	return f(ctx, command, data)
}

func testDevice(id string, exchange exchangeFunc, queueSize int) *Device {
	return NewDevice(id, "usb", "", terminalfa.NewClient(exchange), queueSize)
}

func testService(t *testing.T, devices []*Device, commands []Command) (*Service, *store.Store) {
	t.Helper()
	journal, err := store.Open(filepath.Join(t.TempDir(), "requests.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	return serviceWithJournal(t, devices, commands, journal), journal
}

func serviceWithJournal(t *testing.T, devices []*Device, commands []Command, journal *store.Store) *Service {
	t.Helper()
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	service, err := New(devices, commands, journal, logger)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func responseBody(t *testing.T, reply Reply) Response {
	t.Helper()
	var response Response
	if err := json.Unmarshal(reply.Body, &response); err != nil {
		t.Fatalf("decode reply %q: %v", reply.Body, err)
	}
	return response
}

func requireError(t *testing.T, reply Reply, status int, code string) {
	t.Helper()
	response := responseBody(t, reply)
	if reply.HTTPStatus != status || response.Error == nil || response.Error.Code != code {
		t.Fatalf("got HTTP %d %s; want HTTP %d error %q", reply.HTTPStatus, reply.Body, status, code)
	}
}

func awaitReply(t *testing.T, ch <-chan Reply) Reply {
	t.Helper()
	select {
	case reply := <-ch:
		return reply
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for service reply")
		return Reply{}
	}
}

func awaitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for operation")
	}
}

func awaitSlots(t *testing.T, device *Device, count int) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for len(device.slots) != count {
		select {
		case <-timer.C:
			t.Fatalf("waiting for %d occupied slots; got %d", count, len(device.slots))
		case <-ticker.C:
		}
	}
}

func TestSerialNumberReadsAreFreshAndNeverJournaled(t *testing.T) {
	var calls atomic.Int32
	device := testDevice("kkt-1", func(_ context.Context, command byte, data []byte) ([]byte, error) {
		if command != 0x02 || len(data) != 0 {
			t.Errorf("unexpected protocol request: command=%02X data=% X", command, data)
		}
		if calls.Add(1) == 1 {
			return []byte("550101046182"), nil
		}
		return []byte("550101046183"), nil
	}, 2)
	service, journal := testService(t, []*Device{device}, BuiltinCommands())
	for i, name := range []string{"ID", "getSerialNumber"} {
		reply := service.Execute(context.Background(), Request{DeviceID: device.ID, Command: name, RequestID: "trace-1"})
		response := responseBody(t, reply)
		if reply.HTTPStatus != 200 || response.Status != "completed" || response.Command != "getSerialNumber" || reply.Replayed {
			t.Fatalf("unexpected read response: HTTP %d %s", reply.HTTPStatus, reply.Body)
		}
		var result struct {
			Data SerialNumberResult `json:"data"`
		}
		if err := json.Unmarshal(reply.Body, &result); err != nil {
			t.Fatal(err)
		}
		want := []string{"550101046182", "550101046183"}[i]
		if result.Data.SerialNumber != want {
			t.Errorf("serial number = %q; want %q", result.Data.SerialNumber, want)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("exchange count = %d; want 2", calls.Load())
	}
	if _, err := journal.Get(context.Background(), device.ID, "trace-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("read-only request was persisted: %v", err)
	}
	requireError(t, service.Lookup(context.Background(), device.ID, "trace-1"), 404, "not_found")
}

func TestInvalidRequestsDoNotReachDevice(t *testing.T) {
	var calls atomic.Int32
	device := testDevice("kkt-1", func(context.Context, byte, []byte) ([]byte, error) {
		calls.Add(1)
		return []byte("550101046182"), nil
	}, 2)
	service, _ := testService(t, []*Device{device}, BuiltinCommands())
	tests := []struct {
		name    string
		request Request
		status  int
		code    string
	}{
		{"unknown device", Request{DeviceID: "other", Command: "ID"}, 404, "unknown_device"},
		{"unknown command", Request{DeviceID: device.ID, Command: "WRITE"}, 404, "unknown_command"},
		{"invalid device ID", Request{DeviceID: "../kkt", Command: "ID"}, 400, "invalid_request"},
		{"invalid request ID", Request{DeviceID: device.ID, Command: "ID", RequestID: "bad/id"}, 400, "invalid_request"},
		{"unexpected parameter", Request{DeviceID: device.ID, Command: "ID", Params: json.RawMessage(`{"par1":1}`)}, 400, "invalid_request"},
		{"null params", Request{DeviceID: device.ID, Command: "ID", Params: json.RawMessage(`null`)}, 400, "invalid_request"},
		{"array params", Request{DeviceID: device.ID, Command: "ID", Params: json.RawMessage(`[]`)}, 400, "invalid_request"},
		{"invalid JSON", Request{DeviceID: device.ID, Command: "ID", Params: json.RawMessage(`{`)}, 400, "invalid_request"},
		{"trailing JSON", Request{DeviceID: device.ID, Command: "ID", Params: json.RawMessage(`{} {}`)}, 400, "invalid_request"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireError(t, service.Execute(context.Background(), test.request), test.status, test.code)
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid requests made %d device exchanges", calls.Load())
	}
}

func TestReadErrorsAndPhysicalIdentityCheck(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		err      error
		expected string
		status   int
		code     string
	}{
		{"short serial", []byte("123"), nil, "", 502, "protocol_error"},
		{"non-digit serial", []byte("55010104618X"), nil, "", 502, "protocol_error"},
		{"wrong identity", []byte("550101046182"), nil, "550101046183", 502, "device_identity_mismatch"},
		{"timeout", nil, context.DeadlineExceeded, "", 504, "device_timeout"},
		{"transport", nil, io.ErrUnexpectedEOF, "", 502, "transport_error"},
		{"device rejection", nil, &protocol.DeviceError{Code: 0x26}, "", 502, "device_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			device := testDevice("kkt-1", func(context.Context, byte, []byte) ([]byte, error) {
				return test.data, test.err
			}, 2)
			device.ExpectedSerialNumber = test.expected
			service, _ := testService(t, []*Device{device}, BuiltinCommands())
			reply := service.Execute(context.Background(), Request{DeviceID: device.ID, Command: "ID"})
			requireError(t, reply, test.status, test.code)
			if test.code == "device_error" {
				response := responseBody(t, reply)
				if response.Error.DeviceCode == nil || *response.Error.DeviceCode != 0x26 {
					t.Fatalf("device rejection code not preserved: %s", reply.Body)
				}
			}
		})
	}
}

func TestSameDeviceSerializesRequests(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	device := testDevice("kkt-1", func(ctx context.Context, _ byte, _ []byte) ([]byte, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return []byte("550101046182"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, 2)
	service, _ := testService(t, []*Device{device}, BuiltinCommands())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	replies := make(chan Reply, 2)
	req := Request{DeviceID: device.ID, Command: "ID"}
	go func() { replies <- service.Execute(ctx, req) }()
	awaitSignal(t, entered)
	go func() { replies <- service.Execute(ctx, req) }()
	awaitSlots(t, device, 2)
	select {
	case <-entered:
		t.Fatal("second request entered the same device before the first completed")
	default:
	}
	release <- struct{}{}
	if reply := awaitReply(t, replies); reply.HTTPStatus != 200 {
		t.Fatalf("first request failed: %s", reply.Body)
	}
	awaitSignal(t, entered)
	release <- struct{}{}
	if reply := awaitReply(t, replies); reply.HTTPStatus != 200 {
		t.Fatalf("second request failed: %s", reply.Body)
	}
	awaitSlots(t, device, 0)
}

func TestDifferentDevicesExecuteInParallel(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	defer close(release)
	exchange := func(ctx context.Context, _ byte, _ []byte) ([]byte, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return []byte("550101046182"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	service, _ := testService(t, []*Device{testDevice("kkt-1", exchange, 2), testDevice("kkt-2", exchange, 2)}, BuiltinCommands())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	replies := make(chan Reply, 2)
	for _, id := range []string{"kkt-1", "kkt-2"} {
		go func(id string) { replies <- service.Execute(ctx, Request{DeviceID: id, Command: "ID"}) }(id)
	}
	awaitSignal(t, entered)
	awaitSignal(t, entered)
	// Both operations entered before release; neither holds a global device lock.
	cancel()
	awaitReply(t, replies)
	awaitReply(t, replies)
}

func TestQueueCapacityAndCanceledWaiterReleaseSlots(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	var calls atomic.Int32
	device := testDevice("kkt-1", func(ctx context.Context, _ byte, _ []byte) ([]byte, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return []byte("550101046182"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, 1)
	service, _ := testService(t, []*Device{device}, BuiltinCommands())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := Request{DeviceID: device.ID, Command: "ID"}
	first := make(chan Reply, 1)
	go func() { first <- service.Execute(ctx, req) }()
	awaitSignal(t, entered)
	waitCtx, cancelWait := context.WithCancel(ctx)
	defer cancelWait()
	waiter := make(chan Reply, 1)
	go func() { waiter <- service.Execute(waitCtx, req) }()
	awaitSlots(t, device, 2)
	requireError(t, service.Execute(ctx, req), 503, "queue_full")
	cancelWait()
	requireError(t, awaitReply(t, waiter), 504, "device_timeout")
	awaitSlots(t, device, 1)
	if calls.Load() != 1 {
		t.Fatalf("waiting or rejected request reached device; exchange count=%d", calls.Load())
	}
	release <- struct{}{}
	if reply := awaitReply(t, first); reply.HTTPStatus != 200 {
		t.Fatalf("active request failed: %s", reply.Body)
	}
	awaitSlots(t, device, 0)
	release <- struct{}{}
	if reply := service.Execute(ctx, req); reply.HTTPStatus != 200 {
		t.Fatalf("device unusable after cancellation: %s", reply.Body)
	}
	if calls.Load() != 2 {
		t.Fatalf("exchange count=%d; want 2", calls.Load())
	}
}

// This test-only command exercises future mutation policy without registering
// any fiscal or arbitrary byte command in the production catalogue.
func testMutation(execute func(context.Context, *Device, json.RawMessage) (any, error)) Command {
	return Command{
		Definition: CommandDefinition{Name: "testMutation", Aliases: []string{"TEST_WRITE"}, IdempotencyRequired: true},
		Validate:   func(json.RawMessage) error { return nil },
		Execute:    execute,
	}
}

func unusedDevice(t *testing.T, id string) *Device {
	t.Helper()
	return testDevice(id, func(context.Context, byte, []byte) ([]byte, error) {
		t.Error("unexpected protocol exchange")
		return nil, errors.New("unexpected exchange")
	}, 2)
}

func TestMutationDurableReplayCanonicalAliasAndConflicts(t *testing.T) {
	var calls atomic.Int32
	command := testMutation(func(context.Context, *Device, json.RawMessage) (any, error) {
		return map[string]any{"counter": calls.Add(1)}, nil
	})
	path := filepath.Join(t.TempDir(), "requests.sqlite")
	journal, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	device := unusedDevice(t, "kkt-1")
	service := serviceWithJournal(t, []*Device{device}, []Command{command}, journal)
	req := Request{DeviceID: device.ID, Command: "TEST_WRITE", RequestID: "sale-1", Params: json.RawMessage(`{"b":2,"a":1}`)}
	first := service.Execute(context.Background(), req)
	if first.HTTPStatus != 200 || first.Replayed || responseBody(t, first).Command != "testMutation" {
		t.Fatalf("unexpected initial mutation reply: %s", first.Body)
	}
	record, err := journal.Get(context.Background(), device.ID, req.RequestID)
	if err != nil || record.State != store.StateCompleted || !bytes.Equal(record.Response, first.Body) {
		t.Fatalf("mutation response was not durably completed: %+v %v", record, err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted := serviceWithJournal(t, []*Device{device}, []Command{command}, reopened)
	req.Command = "testMutation"
	req.Params = json.RawMessage(`{ "a": 1, "b": 2 }`)
	replay := restarted.Execute(context.Background(), req)
	if !replay.Replayed || replay.HTTPStatus != first.HTTPStatus || !bytes.Equal(replay.Body, first.Body) || calls.Load() != 1 {
		t.Fatalf("restart/alias replay executed again or changed response: calls=%d reply=%s", calls.Load(), replay.Body)
	}
	lookup := restarted.Lookup(context.Background(), device.ID, req.RequestID)
	if !lookup.Replayed || !bytes.Equal(lookup.Body, first.Body) {
		t.Fatalf("lookup did not return stored response: %s", lookup.Body)
	}
	req.Params = json.RawMessage(`{"a":1,"b":3}`)
	requireError(t, restarted.Execute(context.Background(), req), 409, "idempotency_conflict")
	req.RequestID = ""
	requireError(t, restarted.Execute(context.Background(), req), 400, "invalid_request")
	if calls.Load() != 1 {
		t.Fatalf("conflicting or missing request ID triggered execution; count=%d", calls.Load())
	}
}

func TestConcurrentMutationDuplicateReturnsInProgress(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	command := testMutation(func(ctx context.Context, _ *Device, _ json.RawMessage) (any, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return "done", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	service, _ := testService(t, []*Device{unusedDevice(t, "kkt-1")}, []Command{command})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := Request{DeviceID: "kkt-1", Command: "testMutation", RequestID: "sale-1"}
	first := make(chan Reply, 1)
	go func() { first <- service.Execute(ctx, req) }()
	awaitSignal(t, entered)
	duplicate := service.Execute(ctx, req)
	requireError(t, duplicate, 409, "request_in_progress")
	if responseBody(t, duplicate).Status != "in_progress" {
		t.Fatalf("wrong duplicate state: %s", duplicate.Body)
	}
	close(release)
	completed := awaitReply(t, first)
	if completed.HTTPStatus != 200 || calls.Load() != 1 {
		t.Fatalf("mutation failed or ran more than once: calls=%d body=%s", calls.Load(), completed.Body)
	}
	replayed := service.Execute(context.Background(), req)
	if !replayed.Replayed || !bytes.Equal(replayed.Body, completed.Body) {
		t.Fatalf("completed mutation did not replay: %s", replayed.Body)
	}
}

func TestTimedOutMutationBecomesUnknownAndCannotRetry(t *testing.T) {
	var calls atomic.Int32
	command := testMutation(func(ctx context.Context, _ *Device, _ json.RawMessage) (any, error) {
		calls.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	service, journal := testService(t, []*Device{unusedDevice(t, "kkt-1")}, []Command{command})
	req := Request{DeviceID: "kkt-1", Command: "testMutation", RequestID: "sale-timeout"}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	requireError(t, service.Execute(ctx, req), 409, "outcome_unknown")
	record, err := journal.Get(context.Background(), req.DeviceID, req.RequestID)
	if err != nil || record.State != store.StateUnknown {
		t.Fatalf("timeout not persisted as unknown: %+v %v", record, err)
	}
	if err := journal.RecoverInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireError(t, service.Execute(context.Background(), req), 409, "outcome_unknown")
	requireError(t, service.Lookup(context.Background(), req.DeviceID, req.RequestID), 409, "outcome_unknown")
	if calls.Load() != 1 {
		t.Fatalf("unknown operation was executed again; count=%d", calls.Load())
	}
}

func TestRecoveredInterruptedMutationStaysBlocked(t *testing.T) {
	var calls atomic.Int32
	command := testMutation(func(context.Context, *Device, json.RawMessage) (any, error) {
		calls.Add(1)
		return "unexpected", nil
	})
	service, journal := testService(t, []*Device{unusedDevice(t, "kkt-1")}, []Command{command})
	req := Request{DeviceID: "kkt-1", Command: "testMutation", RequestID: "interrupted"}
	claim, err := journal.Claim(context.Background(), req.DeviceID, req.RequestID, fingerprint(req.Command, json.RawMessage(`{}`)))
	if err != nil || !claim.Acquired {
		t.Fatalf("seed interrupted claim: %+v %v", claim, err)
	}
	if err := journal.RecoverInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireError(t, service.Execute(context.Background(), req), 409, "outcome_unknown")
	if calls.Load() != 0 {
		t.Fatalf("recovered interrupted operation was executed; count=%d", calls.Load())
	}
}

func TestRejectedMutationIsStoredAndReplayed(t *testing.T) {
	var calls atomic.Int32
	command := testMutation(func(context.Context, *Device, json.RawMessage) (any, error) {
		calls.Add(1)
		return nil, &protocol.DeviceError{Code: 0x26}
	})
	service, journal := testService(t, []*Device{unusedDevice(t, "kkt-1")}, []Command{command})
	req := Request{DeviceID: "kkt-1", Command: "testMutation", RequestID: "rejected"}
	first := service.Execute(context.Background(), req)
	requireError(t, first, 502, "device_error")
	replay := service.Execute(context.Background(), req)
	if !replay.Replayed || !bytes.Equal(first.Body, replay.Body) || calls.Load() != 1 {
		t.Fatalf("rejected mutation was executed again: calls=%d body=%s", calls.Load(), replay.Body)
	}
	record, err := journal.Get(context.Background(), req.DeviceID, req.RequestID)
	if err != nil || record.State != store.StateCompleted {
		t.Fatalf("definite rejection not stored as completed: %+v %v", record, err)
	}
}

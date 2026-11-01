package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	terminalfa "github.com/dronm/goterminalfa"
	"github.com/dronm/goterminalfa/internal/protocol"
	"github.com/dronm/goterminalfa/internal/store"
)

func TestClockSyncSamplesAfterQueueAndIdentityAndReplaysAcrossRestart(t *testing.T) {
	zone := time.FixedZone("UTC+05", 5*60*60)
	want := time.Date(2026, 11, 2, 0, 4, 59, 999999999, zone)
	var clock atomic.Value
	clock.Store(want.Add(-24 * time.Hour))
	var samples, identityCalls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	var payloads [][]byte
	device := NewDevice("kkt-1", "tcp", "550101046182", terminalfa.NewClient(exchangeFunc(func(ctx context.Context, code byte, payload []byte) ([]byte, error) {
		switch code {
		case 0x02:
			switch identityCalls.Add(1) {
			case 1:
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			case 2:
				// This time supersedes the value when queued and before identity.
				clock.Store(want)
			}
			return []byte("550101046182"), nil
		case 0x72:
			payloads = append(payloads, append([]byte(nil), payload...))
			return nil, nil
		default:
			t.Errorf("unexpected command %02X", code)
			return nil, errors.New("unexpected command")
		}
	})), 2)
	commands := builtinCommands(func() time.Time {
		samples.Add(1)
		return clock.Load().(time.Time)
	})
	app, journal := testService(t, []*Device{device}, commands)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	blocker := make(chan Reply, 1)
	go func() { blocker <- app.Execute(ctx, Request{DeviceID: device.ID, Command: "ID"}) }()
	awaitSignal(t, entered)
	req := Request{DeviceID: device.ID, Command: "syncDateTime", RequestID: "clock-sync-1"}
	result := make(chan Reply, 1)
	go func() { result <- app.Execute(ctx, req) }()
	awaitSlots(t, device, 2)
	if samples.Load() != 0 {
		t.Fatal("clock sampled before obtaining device ownership")
	}
	clock.Store(want.Add(-time.Hour))
	close(release)
	if reply := awaitReply(t, blocker); reply.HTTPStatus != 200 {
		t.Fatalf("blocking read failed: %s", reply.Body)
	}
	first := awaitReply(t, result)
	if first.HTTPStatus != 200 || samples.Load() != 1 || len(payloads) != 1 {
		t.Fatalf("sync failed: samples %d payloads %d reply %s", samples.Load(), len(payloads), first.Body)
	}
	var body struct {
		Data clockSyncResult `json:"data"`
	}
	if err := json.Unmarshal(first.Body, &body); err != nil || body.Data.DateTime != "2026-11-02T00:04" {
		t.Fatalf("wrong local calendar or minute precision: %s, %v", first.Body, err)
	}
	if want := []byte{0x30, 0x75, 5, 0, 26, 11, 2, 0, 4}; !bytes.Equal(payloads[0], want) {
		t.Fatalf("0x72 TLV payload % X; want % X", payloads[0], want)
	}
	record, err := journal.Get(ctx, device.ID, req.RequestID)
	if err != nil || record.State != store.StateCompleted || string(record.Params) != "{}" {
		t.Fatalf("runtime clock changed canonical request: %+v, %v", record, err)
	}
	clock.Store(want.Add(time.Hour))
	restarted := serviceWithJournal(t, []*Device{device}, builtinCommands(func() time.Time {
		t.Error("saved request sampled a new server clock after restart")
		return want.Add(24 * time.Hour)
	}), journal)
	req.Params = json.RawMessage(`{ }`)
	replay := restarted.Execute(ctx, req)
	if !replay.Replayed || replay.HTTPStatus != 200 || !bytes.Equal(replay.Body, first.Body) || len(payloads) != 1 || identityCalls.Load() != 2 {
		t.Fatalf("replay changed time or accessed device: %s", replay.Body)
	}
	req.RequestID = "clock-sync-2"
	if reply := app.Execute(ctx, req); reply.HTTPStatus != 200 || samples.Load() != 2 || len(payloads) != 2 || payloads[1][7] != 1 {
		t.Fatalf("new intent did not sample current time: %s, % X", reply.Body, payloads)
	}
}

func TestClockSyncDateRangeAndDefiniteLocalFailure(t *testing.T) {
	for _, year := range []int{1999, 2000, 2255, 2256} {
		t.Run(time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).Format("2006"), func(t *testing.T) {
			calls := 0
			device := testDevice("kkt-1", func(_ context.Context, code byte, payload []byte) ([]byte, error) {
				calls++
				if code != 0x72 || len(payload) != 9 || payload[4] != byte(year-2000) {
					t.Fatalf("incorrect clock payload: %02X % X", code, payload)
				}
				return nil, nil
			}, 1)
			app, journal := testService(t, []*Device{device}, builtinCommands(func() time.Time {
				return time.Date(year, 12, 31, 23, 59, 59, 0, time.UTC)
			}))
			req := Request{DeviceID: device.ID, Command: "syncDateTime", RequestID: "clock-range"}
			first := app.Execute(context.Background(), req)
			if year == 1999 || year == 2256 {
				requireError(t, first, 502, "transport_error")
				if calls != 0 || responseBody(t, first).Status != "failed" {
					t.Fatalf("unrepresentable host clock reached device: %s", first.Body)
				}
			} else if first.HTTPStatus != 200 || calls != 1 {
				t.Fatalf("valid boundary failed: %s", first.Body)
			}
			record, err := journal.Get(context.Background(), device.ID, req.RequestID)
			if err != nil || record.State != store.StateCompleted {
				t.Fatalf("clock outcome is not definite: %+v, %v", record, err)
			}
			replay := app.Execute(context.Background(), req)
			if !replay.Replayed || !bytes.Equal(replay.Body, first.Body) {
				t.Fatalf("clock result did not replay: %s", replay.Body)
			}
		})
	}
}

func TestClockSyncPreservesDeviceAndDeliveryFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
		state  string
	}{
		{"rejection", &protocol.DeviceError{Code: 0x26}, 502, "device_error", store.StateCompleted},
		{"not sent", &terminalfa.CommandNotSentError{Err: errors.New("connection refused")}, 502, "transport_error", store.StateCompleted},
		{"lost reply", io.ErrUnexpectedEOF, 409, "outcome_unknown", store.StateUnknown},
		{"timeout", context.DeadlineExceeded, 409, "outcome_unknown", store.StateUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls, samples := 0, 0
			device := testDevice("kkt-1", func(_ context.Context, code byte, _ []byte) ([]byte, error) {
				calls++
				if code != 0x72 {
					t.Fatalf("unexpected command %02X", code)
				}
				return nil, test.err
			}, 1)
			app, journal := testService(t, []*Device{device}, builtinCommands(func() time.Time {
				samples++
				return time.Date(2026, 11, 1, 12, 34, 0, 0, time.UTC)
			}))
			req := Request{DeviceID: device.ID, Command: "syncDateTime", RequestID: "failed-clock"}
			requireError(t, app.Execute(context.Background(), req), test.status, test.code)
			record, err := journal.Get(context.Background(), device.ID, req.RequestID)
			if err != nil || record.State != test.state {
				t.Fatalf("incorrect outcome: %+v, %v", record, err)
			}
			requireError(t, app.Execute(context.Background(), req), test.status, test.code)
			if calls != 1 || samples != 1 {
				t.Fatalf("failed synchronization repeated: %d calls, %d samples", calls, samples)
			}
		})
	}
}

func TestClockSyncBatchValidationAndMutationTracking(t *testing.T) {
	for _, test := range []struct {
		name, params string
		invalidClock bool
		wantStatus   int
		wantCodes    []byte
	}{
		{"sync then failure", `{"commands":[{"cmd":"syncDateTime"},{"cmd":"resetCashCounters"}]}`, false, 409, []byte{0x72, 0xB1}},
		{"prior mutation then invalid clock", `{"commands":[{"cmd":"resetCashCounters"},{"cmd":"syncDateTime"}]}`, true, 409, []byte{0xB1}},
		{"sync then invalid input", `{"commands":[{"cmd":"syncDateTime"},{"cmd":"setDateTime","params":{"dateTime":"invalid"}}]}`, false, 400, nil},
		{"sync alone in operation", `{"commands":[{"cmd":"syncDateTime"}]}`, false, 200, []byte{0x72}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var codes []byte
			samples := 0
			device := testDevice("kkt-1", func(_ context.Context, code byte, _ []byte) ([]byte, error) {
				codes = append(codes, code)
				if code == 0xB1 && !test.invalidClock {
					return nil, &terminalfa.CommandNotSentError{Err: errors.New("connection refused")}
				}
				return nil, nil
			}, 1)
			app, journal := testService(t, []*Device{device}, builtinCommands(func() time.Time {
				samples++
				year := 2026
				if test.invalidClock {
					year = 1999
				}
				return time.Date(year, 11, 1, 12, 34, 0, 0, time.UTC)
			}))
			req := Request{DeviceID: device.ID, Command: "executeOperation", RequestID: "clock-operation", Params: json.RawMessage(test.params)}
			reply := app.Execute(context.Background(), req)
			if reply.HTTPStatus != test.wantStatus || !bytes.Equal(codes, test.wantCodes) {
				t.Fatalf("wrong operation outcome: %s, commands % X", reply.Body, codes)
			}
			if test.wantStatus == 400 {
				if samples != 0 {
					t.Fatal("invalid sequence sampled the clock")
				}
				if _, err := journal.Get(context.Background(), device.ID, req.RequestID); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("invalid operation was claimed: %v", err)
				}
			} else if samples != 1 {
				t.Fatalf("clock sampled %d times", samples)
			}
			if test.wantStatus == 409 {
				requireError(t, reply, 409, "outcome_unknown")
				record, err := journal.Get(context.Background(), device.ID, req.RequestID)
				if err != nil || record.State != store.StateUnknown {
					t.Fatalf("partial mutation lost unknown claim: %+v, %v", record, err)
				}
				requireError(t, app.Execute(context.Background(), req), 409, "outcome_unknown")
				if samples != 1 || !bytes.Equal(codes, test.wantCodes) {
					t.Fatal("unknown operation repeated")
				}
			}
		})
	}
}

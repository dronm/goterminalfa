package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	terminalfa "github.com/dronm/goterminalfa"
	"github.com/dronm/goterminalfa/internal/store"
)

func TestTCPPreSendFailureIsDefiniteAndDurablyReplayed(t *testing.T) {
	calls := 0
	device := NewDevice("kkt-1", "tcp", "", terminalfa.NewClient(exchangeFunc(func(context.Context, byte, []byte) ([]byte, error) {
		calls++
		return nil, &terminalfa.CommandNotSentError{Err: errors.New("connection refused")}
	})), 1)
	app, journal := testService(t, []*Device{device}, BuiltinCommands())
	req := Request{DeviceID: device.ID, Command: "setDateTime", RequestID: "not-sent-1", Params: json.RawMessage(`{"dateTime":"2026-11-01T12:34"}`)}
	first := app.Execute(context.Background(), req)
	requireError(t, first, 502, "transport_error")
	if responseBody(t, first).Status != "failed" {
		t.Fatalf("unsent command was not a definite failure: %s", first.Body)
	}
	record, err := journal.Get(context.Background(), device.ID, req.RequestID)
	if err != nil || record.State != store.StateCompleted {
		t.Fatalf("record state: %+v, %v", record, err)
	}
	second := app.Execute(context.Background(), req)
	if !second.Replayed || string(second.Body) != string(first.Body) || calls != 1 {
		t.Fatalf("unsent failure was retried: calls %d, %s", calls, second.Body)
	}
}

func TestLaterTCPPreSendFailureCannotHideEarlierMutation(t *testing.T) {
	calls := 0
	device := NewDevice("kkt-1", "tcp", "", terminalfa.NewClient(exchangeFunc(func(_ context.Context, code byte, _ []byte) ([]byte, error) {
		calls++
		if code == 0x72 {
			return nil, nil
		}
		return nil, &terminalfa.CommandNotSentError{Err: errors.New("connection refused")}
	})), 1)
	app, journal := testService(t, []*Device{device}, BuiltinCommands())
	req := Request{DeviceID: device.ID, Command: "executeOperation", RequestID: "partially-done", Params: json.RawMessage(`{"commands":[{"cmd":"setDateTime","params":{"dateTime":"2026-11-01T12:34"}},{"cmd":"resetCashCounters"}]}`)}
	reply := app.Execute(context.Background(), req)
	requireError(t, reply, 409, "outcome_unknown")
	record, err := journal.Get(context.Background(), device.ID, req.RequestID)
	if err != nil || record.State != store.StateUnknown {
		t.Fatalf("record state: %+v, %v", record, err)
	}
	requireError(t, app.Execute(context.Background(), req), 409, "outcome_unknown")
	if calls != 2 {
		t.Fatalf("partial operation was resent: %d calls", calls)
	}
}

func TestTCPDeviceEndpointChangePreflight(t *testing.T) {
	device := NewDevice("kkt-1", "tcp", "", terminalfa.NewClient(exchangeFunc(func(context.Context, byte, []byte) ([]byte, error) {
		t.Fatal("endpoint-changing invalid sequence reached device")
		return nil, nil
	})), 1)
	app, journal := testService(t, []*Device{device}, BuiltinCommands())
	for _, params := range []string{
		`{"commands":[{"cmd":"setNetworkSettings","params":{"dynamicAddress":0,"ipAddress":"192.0.2.50","subnetMask":"255.255.255.0","gateway":"192.0.2.1"}},{"cmd":"getNetworkSettings"}]}`,
		`{"commands":[{"cmd":"setControlSettings","params":{"controlInterface":0,"tcpPort":7778}},{"cmd":"getControlSettings"}]}`,
	} {
		req := Request{DeviceID: device.ID, Command: "executeOperation", RequestID: "bad-migration", Params: json.RawMessage(params)}
		requireError(t, app.Execute(context.Background(), req), 400, "invalid_request")
		if _, err := journal.Get(context.Background(), device.ID, req.RequestID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("invalid batch was claimed: %v", err)
		}
	}
}

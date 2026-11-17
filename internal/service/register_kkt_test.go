package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/dronm/goterminalfa/internal/protocol"
	"github.com/dronm/goterminalfa/internal/store"
)

func registrationAppRequest(t *testing.T) Request {
	t.Helper()
	return Request{DeviceID: "kkt-1", Command: "registerKkt", RequestID: "registration-1", Params: json.RawMessage(`{
		"reportType":0,"userName":"User","settlementAddress":"Address","settlementPlace":"Place",
		"representativeName":"Representative","ofdInn":"7707083893","ofdName":"OFD","senderEmail":"Receipt@example.org",
		"userInn":"7707083893","registrationNumber":"0000000001046446","taxationSystems":1
	}`)}
}

func registrationAppResponse(code byte) []byte {
	if code == 0xE3 {
		return []byte{9, 0, 0, 0, 7, 0, 0, 0}
	}
	return receiptTestResponse(code, 0)
}

func TestRegisterKktOwnsOneSerialSessionAndReplaysAcrossServiceRestart(t *testing.T) {
	for _, expected := range []string{"", "550101046182"} {
		t.Run("expected="+expected, func(t *testing.T) {
			transport := &receiptTestTransport{respond: func(_ context.Context, call receiptWireCall) ([]byte, error) {
				return registrationAppResponse(call.Code), nil
			}}
			app, device, journal := receiptTestApp(t, transport, expected)
			req := registrationAppRequest(t)
			first := app.Execute(context.Background(), req)
			if first.HTTPStatus != 200 {
				t.Fatalf("registration failed: %s", first.Body)
			}
			if got := receiptCalls(transport); !bytes.Equal(got, []byte{0x02, 0xE1, 0xE2, 0xE3}) {
				t.Fatalf("wire commands = %X", got)
			}
			if transport.opens != 1 || transport.closes != 1 {
				t.Fatalf("physical sessions opened=%d closed=%d; want one", transport.opens, transport.closes)
			}
			for _, call := range transport.calls {
				if call.Session != 1 {
					t.Fatalf("registration crossed serial sessions: %+v", transport.calls)
				}
			}
			if got := receiptReplyData(t, first); got["documentNumber"] != float64(9) || got["fiscalSign"] != float64(7) || len(got) != 2 {
				t.Fatalf("unexpected registration result: %s", first.Body)
			}
			restarted := serviceWithJournal(t, []*Device{device}, BuiltinCommands(), journal)
			replay := restarted.Execute(context.Background(), req)
			if !replay.Replayed || !bytes.Equal(first.Body, replay.Body) || len(receiptCalls(transport)) != 4 {
				t.Fatalf("restart repeated a completed registration: %s", replay.Body)
			}
		})
	}
}

func TestRegisterKktRetainsDeviceGateAcrossEntireProcedure(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	transport := &receiptTestTransport{respond: func(ctx context.Context, call receiptWireCall) ([]byte, error) {
		if call.Code == 0xE1 {
			entered <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return registrationAppResponse(call.Code), nil
	}}
	app, device, _ := receiptTestApp(t, transport, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	registration := make(chan Reply, 1)
	other := make(chan Reply, 1)
	go func() { registration <- app.Execute(ctx, registrationAppRequest(t)) }()
	awaitSignal(t, entered)
	go func() { other <- app.Execute(ctx, Request{DeviceID: device.ID, Command: "getSerialNumber"}) }()
	awaitSlots(t, device, 2)
	if got := receiptCalls(transport); !bytes.Equal(got, []byte{0x02, 0xE1}) {
		t.Fatalf("a competing request interleaved with registration: %X", got)
	}
	close(release)
	if reply := awaitReply(t, registration); reply.HTTPStatus != 200 {
		t.Fatalf("registration failed: %s", reply.Body)
	}
	if reply := awaitReply(t, other); reply.HTTPStatus != 200 {
		t.Fatalf("queued request failed: %s", reply.Body)
	}
	if got := receiptCalls(transport); !bytes.Equal(got, []byte{0x02, 0xE1, 0xE2, 0xE3, 0x02}) {
		t.Fatalf("wire commands interleaved: %X", got)
	}
}

func TestRegisterKktFailureClaimsRemainDurable(t *testing.T) {
	for _, failedCode := range []byte{0xE1, 0xE2, 0xE3} {
		t.Run(fmt.Sprintf("command=0x%02X", failedCode), func(t *testing.T) {
			transport := &receiptTestTransport{respond: func(_ context.Context, call receiptWireCall) ([]byte, error) {
				if call.Code == failedCode {
					return nil, &protocol.DeviceError{Code: 0x26}
				}
				return registrationAppResponse(call.Code), nil
			}}
			app, device, journal := receiptTestApp(t, transport, "")
			req := registrationAppRequest(t)
			first := app.Execute(context.Background(), req)
			wantState := store.StateUnknown
			if failedCode == 0xE1 {
				requireError(t, first, 502, "device_error")
				wantState = store.StateCompleted
			} else {
				requireError(t, first, 409, "outcome_unknown")
			}
			count := len(receiptCalls(transport))
			record, err := journal.Get(context.Background(), device.ID, req.RequestID)
			if err != nil || record.State != wantState {
				t.Fatalf("registration journal state=%s error=%v; want %s", record.State, err, wantState)
			}
			restarted := serviceWithJournal(t, []*Device{device}, BuiltinCommands(), journal)
			replay := restarted.Execute(context.Background(), req)
			if !bytes.Equal(first.Body, replay.Body) || count != len(receiptCalls(transport)) {
				t.Fatalf("restart resent a failed registration: %s", replay.Body)
			}
		})
	}
}

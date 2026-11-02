package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	terminalfa "github.com/dronm/goterminalfa"
	"github.com/dronm/goterminalfa/internal/protocol"
	"github.com/dronm/goterminalfa/internal/store"
)

type receiptWireCall struct {
	Code    byte
	Payload []byte
	Session int
}

// This fake models the production session contract: the original session is
// unusable after any exchange failure or parent cancellation. Cleanup must open
// a fresh session rather than reusing a canceled/scoped client.
type receiptTestTransport struct {
	mu      sync.Mutex
	calls   []receiptWireCall
	events  []string
	opens   int
	closes  int
	state   byte
	respond func(context.Context, receiptWireCall) ([]byte, error)
}

type receiptTestSession struct {
	transport *receiptTestTransport
	ctx       context.Context
	id        int
	closed    bool
	failed    bool
}

func (x *receiptTestTransport) Exchange(ctx context.Context, code byte, payload []byte) (data []byte, err error) {
	err = x.WithSession(ctx, func(session terminalfa.Exchanger) error {
		data, err = session.Exchange(ctx, code, payload)
		return err
	})
	return data, err
}

func (x *receiptTestTransport) WithSession(ctx context.Context, callback func(terminalfa.Exchanger) error) error {
	x.mu.Lock()
	x.opens++
	id := x.opens
	x.events = append(x.events, fmt.Sprintf("open:%d", id))
	x.mu.Unlock()
	session := &receiptTestSession{transport: x, ctx: ctx, id: id}
	defer func() {
		session.closed = true
		x.mu.Lock()
		x.closes++
		x.events = append(x.events, fmt.Sprintf("close:%d", id))
		x.mu.Unlock()
	}()
	return callback(session)
}

func (s *receiptTestSession) Exchange(ctx context.Context, code byte, payload []byte) ([]byte, error) {
	if s.closed || s.failed {
		return nil, errors.New("receipt test session is closed or failed")
	}
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	call := receiptWireCall{Code: code, Payload: append([]byte{}, payload...), Session: s.id}
	s.transport.mu.Lock()
	s.transport.calls = append(s.transport.calls, call)
	s.transport.mu.Unlock()
	var data []byte
	var err error
	if s.transport.respond != nil {
		data, err = s.transport.respond(ctx, call)
	} else {
		data = receiptTestResponse(code, s.transport.state)
	}
	if err != nil {
		s.failed = true
	}
	return data, err
}

func receiptTestResponse(code, shiftState byte) []byte {
	switch code {
	case 0x02:
		return []byte("550101046182")
	case 0x20:
		return []byte{shiftState, 4, 0, 2, 0}
	case 0x22:
		return []byte{5, 0, 6, 0, 0, 0, 7, 0, 0, 0, 0}
	case 0x24:
		return []byte{1, 0, 7, 0, 0, 0, 9, 0, 0, 0, 26, 11, 2, 10, 30, 5, 0}
	default:
		return nil
	}
}

func receiptTestParams() map[string]any {
	return map[string]any{
		"cashierName": "Cashier", "calculationSign": 1, "totalKopecks": 100,
		"items":          []any{map[string]any{"name": "Item", "priceKopecks": 100, "quantity": "1", "vatRate": 11, "paymentMethod": 4, "paymentObject": 1}},
		"payment":        map[string]any{"taxSystem": 1, "cashKopecks": 100, "cashlessKopecks": 0, "prepaymentKopecks": 0, "creditKopecks": 0, "counterProvisionKopecks": 0},
		"additionalData": map[string]any{"cashierName": "Receipt cashier"},
	}
}

func receiptTestRequest(t *testing.T, params map[string]any) Request {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return Request{DeviceID: "kkt-1", Command: "createReceipt", RequestID: "receipt-1", Params: raw}
}

func receiptTestApp(t *testing.T, transport *receiptTestTransport, expectedSerial string) (*Service, *Device, *store.Store) {
	t.Helper()
	device := NewDevice("kkt-1", "serial", expectedSerial, terminalfa.NewClient(transport), 2)
	device.ProtocolProfile = "ffd1.2"
	app, journal := testService(t, []*Device{device}, BuiltinCommands())
	return app, device, journal
}

func receiptReplyData(t *testing.T, reply Reply) map[string]any {
	t.Helper()
	var response struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(reply.Body, &response); err != nil {
		t.Fatal(err)
	}
	if response.Data == nil {
		t.Fatalf("receipt response omitted execution details: %s", reply.Body)
	}
	return response.Data
}

func receiptCalls(x *receiptTestTransport) []byte {
	x.mu.Lock()
	defer x.mu.Unlock()
	codes := make([]byte, len(x.calls))
	for i, call := range x.calls {
		codes[i] = call.Code
	}
	return codes
}

func requireReceiptCleanup(t *testing.T, reply Reply, attempted, succeeded bool) map[string]any {
	t.Helper()
	data := receiptReplyData(t, reply)
	cleanup, ok := data["cleanup"].(map[string]any)
	if !ok || cleanup["attempted"] != attempted || cleanup["succeeded"] != succeeded {
		t.Fatalf("unexpected cleanup state: %s", reply.Body)
	}
	if data["failedStage"] == nil || data["failedCommand"] == nil {
		t.Fatalf("failed receipt omitted failedStage/failedCommand: %s", reply.Body)
	}
	return cleanup
}

func TestCreateReceiptOpensShiftOnlyWhenNeededAndReplays(t *testing.T) {
	for _, state := range []byte{0, 1} {
		for _, expected := range []string{"", "550101046182"} {
			t.Run(fmt.Sprintf("state=%d/expectedSerial=%s", state, expected), func(t *testing.T) {
				transport := &receiptTestTransport{state: state}
				app, _, journal := receiptTestApp(t, transport, expected)
				params := receiptTestParams()
				params["cashierInn"] = "123456789012"
				params["suppressShiftPrint"] = 1
				params["printSuppressionCode"] = 41029
				req := receiptTestRequest(t, params)
				first := app.Execute(context.Background(), req)
				if first.HTTPStatus != 200 {
					t.Fatalf("createReceipt failed: %s", first.Body)
				}
				want := []byte{0x02, 0x20, 0x23, 0x96, 0x98, 0x2D, 0x24}
				if state == 0 {
					want = []byte{0x02, 0x20, 0x21, 0x2F, 0x22, 0x23, 0x96, 0x98, 0x2D, 0x24}
				}
				if got := receiptCalls(transport); !bytes.Equal(got, want) {
					t.Fatalf("wire commands %X, expected %X", got, want)
				}
				data := receiptReplyData(t, first)
				if data["shiftOpenedByRequest"] != (state == 0) || data["receipt"] == nil {
					t.Fatalf("missing receipt/shift details: %s", first.Body)
				}
				if state == 0 && data["shiftResult"] == nil {
					t.Fatalf("new shift result was omitted: %s", first.Body)
				}
				for _, call := range transport.calls {
					if call.Session != 1 {
						t.Fatal("successful createReceipt reopened its primary connection")
					}
					if call.Code == 0x21 && !bytes.Equal(call.Payload, []byte{1}) {
						t.Fatalf("suppressShiftPrint not encoded: %X", call.Payload)
					}
					if call.Code == 0x23 && !bytes.Equal(call.Payload, []byte{0x45, 0xA0}) {
						t.Fatalf("receipt printing parameter not encoded: %X", call.Payload)
					}
				}
				second := app.Execute(context.Background(), req)
				if !second.Replayed || !bytes.Equal(first.Body, second.Body) || !bytes.Equal(receiptCalls(transport), want) {
					t.Fatalf("successful receipt was printed twice: %s", second.Body)
				}
				record, err := journal.Get(context.Background(), req.DeviceID, req.RequestID)
				if err != nil || record.State != store.StateCompleted || !bytes.Equal(record.Response, first.Body) {
					t.Fatalf("receipt completion was not persisted: %+v %v", record, err)
				}
				if transport.opens != 1 || transport.closes != 1 {
					t.Fatalf("wrong session lifetime: opens=%d closes=%d", transport.opens, transport.closes)
				}
			})
		}
	}
}

func TestCreateReceiptValidatesEveryBranchBeforeIO(t *testing.T) {
	cases := map[string]func(map[string]any){
		"missing cashier":                      func(p map[string]any) { delete(p, "cashierName") },
		"oversized cashier even on open shift": func(p map[string]any) { p["cashierName"] = strings.Repeat("X", 65) },
		"bad cashier INN":                      func(p map[string]any) { p["cashierInn"] = "123" },
		"invalid shift printing":               func(p map[string]any) { p["suppressShiftPrint"] = 2 },
		"invalid receipt printing":             func(p map[string]any) { p["printSuppressionCode"] = 1 },
		"empty items":                          func(p map[string]any) { p["items"] = []any{} },
		"too many items": func(p map[string]any) {
			item := p["items"].([]any)[0]
			items := make([]any, 201)
			for i := range items {
				items[i] = item
			}
			p["items"] = items
		},
		"missing items":         func(p map[string]any) { delete(p, "items") },
		"float quantity":        func(p map[string]any) { p["items"].([]any)[0].(map[string]any)["quantity"] = 1.5 },
		"invalid item encoding": func(p map[string]any) { p["items"].([]any)[0].(map[string]any)["name"] = "😀" },
		"invalid later item": func(p map[string]any) {
			p["items"] = append(p["items"].([]any), map[string]any{"name": "Later", "priceKopecks": 100, "quantity": 1.5, "vatRate": 11, "paymentMethod": 4, "paymentObject": 1})
		},
		"invalid payment":          func(p map[string]any) { p["payment"] = map[string]any{"taxSystem": 1} },
		"invalid additional data":  func(p map[string]any) { p["additionalData"] = map[string]any{"unknownField": 1} },
		"invalid calculation sign": func(p map[string]any) { p["calculationSign"] = 0 },
		"missing total":            func(p map[string]any) { delete(p, "totalKopecks") },
		"negative total":           func(p map[string]any) { p["totalKopecks"] = -1 },
		"unknown field":            func(p map[string]any) { p["unexpected"] = true },
	}
	for name, modify := range cases {
		t.Run(name, func(t *testing.T) {
			transport := &receiptTestTransport{state: 1}
			app, _, journal := receiptTestApp(t, transport, "550101046182")
			params := receiptTestParams()
			modify(params)
			req := receiptTestRequest(t, params)
			requireError(t, app.Execute(context.Background(), req), 400, "invalid_request")
			if len(receiptCalls(transport)) != 0 || transport.opens != 0 {
				t.Fatal("invalid request reached identity or hardware access")
			}
			if _, err := journal.Get(context.Background(), req.DeviceID, req.RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("invalid receipt consumed its request ID: %v", err)
			}
		})
	}
	transport := &receiptTestTransport{state: 1}
	app, _, _ := receiptTestApp(t, transport, "")
	req := receiptTestRequest(t, receiptTestParams())
	req.RequestID = ""
	requireError(t, app.Execute(context.Background(), req), 400, "invalid_request")
	if transport.opens != 0 {
		t.Fatal("receipt without idempotency key opened hardware")
	}
}

func TestCreateReceiptDefiniteAndUncertainFailureCleanup(t *testing.T) {
	cases := []struct {
		name          string
		state         byte
		failCode      byte
		failErr       error
		cancelErr     error
		wantStatus    int
		wantCode      string
		wantCancel    bool
		wantSucceeded bool
		wantShiftOpen bool
	}{
		{"item rejected", 1, 0x96, &protocol.DeviceError{Code: 0x28}, nil, 502, "device_error", true, true, false},
		{"begin rejected", 1, 0x23, &protocol.DeviceError{Code: 0x02}, nil, 502, "device_error", false, false, false},
		{"begin response lost", 1, 0x23, io.ErrUnexpectedEOF, nil, 409, "outcome_unknown", false, false, false},
		{"final response lost", 1, 0x24, io.ErrUnexpectedEOF, nil, 409, "outcome_unknown", true, true, false},
		{"final CRC corrupted", 1, 0x24, protocol.ErrChecksumMismatch, nil, 409, "outcome_unknown", true, true, false},
		{"cancel rejected", 1, 0x96, &protocol.DeviceError{Code: 0x28}, &protocol.DeviceError{Code: 0x02}, 409, "outcome_unknown", true, false, false},
		{"cashier rejected in owned shift report", 0, 0x2F, &protocol.DeviceError{Code: 0x28}, nil, 502, "device_error", true, true, false},
		{"shift completion response lost", 0, 0x22, io.ErrUnexpectedEOF, nil, 409, "outcome_unknown", true, true, false},
		{"shift opening rejected", 0, 0x21, &protocol.DeviceError{Code: 0x02}, nil, 502, "device_error", false, false, false},
		{"item rejected after opening shift", 0, 0x96, &protocol.DeviceError{Code: 0x28}, nil, 502, "device_error", true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := &receiptTestTransport{state: tc.state}
			transport.respond = func(_ context.Context, call receiptWireCall) ([]byte, error) {
				if call.Code == 0x10 {
					return nil, tc.cancelErr
				}
				if call.Code == tc.failCode {
					return nil, tc.failErr
				}
				return receiptTestResponse(call.Code, tc.state), nil
			}
			app, _, journal := receiptTestApp(t, transport, "")
			req := receiptTestRequest(t, receiptTestParams())
			first := app.Execute(context.Background(), req)
			requireError(t, first, tc.wantStatus, tc.wantCode)
			cleanup := requireReceiptCleanup(t, first, tc.wantCancel, tc.wantSucceeded)
			data := receiptReplyData(t, first)
			if data["failedCommand"] != fmt.Sprintf("0x%02X", tc.failCode) || data["shiftOpenedByRequest"] != tc.wantShiftOpen {
				t.Fatalf("wrong failed-stage/shift details: %s", first.Body)
			}
			if !tc.wantCancel && cleanup["skippedReason"] == nil {
				t.Fatalf("skipped cleanup has no reason: %s", first.Body)
			}
			if tc.cancelErr != nil && cleanup["error"] == nil {
				t.Fatalf("cleanup failure omitted error details: %s", first.Body)
			}
			codes := receiptCalls(transport)
			if bytes.Contains(codes, []byte{0x10}) != tc.wantCancel {
				t.Fatalf("unexpected cancel behavior: commands=%X", codes)
			}
			if tc.wantCancel {
				if transport.opens != 2 || transport.closes != 2 || !strings.Contains(strings.Join(transport.events, ","), "close:1,open:2") {
					t.Fatalf("cleanup reused the failed primary session: events=%v", transport.events)
				}
				last := transport.calls[len(transport.calls)-2:]
				if last[0].Code != 0x02 || last[1].Code != 0x10 || last[0].Session != 2 || last[1].Session != 2 {
					t.Fatalf("cleanup did not verify identity in its own session: %+v", last)
				}
			}
			record, err := journal.Get(context.Background(), req.DeviceID, req.RequestID)
			if err != nil || !bytes.Equal(record.Response, first.Body) {
				t.Fatalf("receipt failure details not durably retained: %+v %v", record, err)
			}
			if (record.State == store.StateUnknown) != (tc.wantStatus == 409) {
				t.Fatalf("wrong durable outcome state: %s", record.State)
			}
			second := app.Execute(context.Background(), req)
			if !bytes.Equal(first.Body, second.Body) || !bytes.Equal(codes, receiptCalls(transport)) {
				t.Fatalf("failed/unknown receipt was retried or lost details: %s", second.Body)
			}
			lookup := app.Lookup(context.Background(), req.DeviceID, req.RequestID)
			if !bytes.Equal(first.Body, lookup.Body) || lookup.HTTPStatus != first.HTTPStatus {
				t.Fatalf("lookup lost failed-receipt cleanup details: %s", lookup.Body)
			}
		})
	}
}

func TestCreateReceiptCancellationCleanupUsesFreshContext(t *testing.T) {
	itemEntered := make(chan struct{})
	transport := &receiptTestTransport{state: 1}
	transport.respond = func(ctx context.Context, call receiptWireCall) ([]byte, error) {
		if call.Code == 0x96 {
			close(itemEntered)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		if call.Code == 0x10 && ctx.Err() != nil {
			t.Error("cleanup inherited the canceled HTTP context")
		}
		return receiptTestResponse(call.Code, 1), nil
	}
	app, _, _ := receiptTestApp(t, transport, "")
	req := receiptTestRequest(t, receiptTestParams())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Reply, 1)
	go func() { done <- app.Execute(ctx, req) }()
	awaitSignal(t, itemEntered)
	cancel()
	reply := awaitReply(t, done)
	requireError(t, reply, 409, "outcome_unknown")
	requireReceiptCleanup(t, reply, true, true)
	if got := receiptCalls(transport); !bytes.Equal(got, []byte{0x02, 0x20, 0x23, 0x96, 0x02, 0x10}) {
		t.Fatalf("canceled receipt cleanup sequence=%X", got)
	}
}

func TestCreateReceiptRetainsDeviceGateThroughCleanup(t *testing.T) {
	cleanupEntered, releaseCleanup := make(chan struct{}), make(chan struct{})
	transport := &receiptTestTransport{state: 1}
	transport.respond = func(ctx context.Context, call receiptWireCall) ([]byte, error) {
		if call.Code == 0x96 {
			return nil, &protocol.DeviceError{Code: 0x28}
		}
		if call.Code == 0x10 {
			close(cleanupEntered)
			select {
			case <-releaseCleanup:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return receiptTestResponse(call.Code, 1), nil
	}
	app, device, _ := receiptTestApp(t, transport, "")
	first := make(chan Reply, 1)
	go func() { first <- app.Execute(context.Background(), receiptTestRequest(t, receiptTestParams())) }()
	awaitSignal(t, cleanupEntered)
	second := make(chan Reply, 1)
	go func() {
		second <- app.Execute(context.Background(), Request{DeviceID: device.ID, Command: "getShiftParameters"})
	}()
	awaitSlots(t, device, 2)
	select {
	case reply := <-second:
		t.Fatalf("another request escaped the cleanup lock: %s", reply.Body)
	case <-time.After(15 * time.Millisecond):
	}
	close(releaseCleanup)
	requireError(t, awaitReply(t, first), 502, "device_error")
	if reply := awaitReply(t, second); reply.HTTPStatus != 200 {
		t.Fatalf("queued read failed: %s", reply.Body)
	}
	got := receiptCalls(transport)
	if !bytes.Equal(got, []byte{0x02, 0x20, 0x23, 0x96, 0x02, 0x10, 0x20}) {
		t.Fatalf("request interleaved with cleanup: %X", got)
	}
}

func TestCreateReceiptIsFFDOnlyAndCannotBeNested(t *testing.T) {
	base := unusedDevice(t, "kkt-1")
	app, _ := testService(t, []*Device{base}, BuiltinCommands())
	req := receiptTestRequest(t, receiptTestParams())
	requireError(t, app.Execute(context.Background(), req), 404, "unknown_command")
	transport := &receiptTestTransport{state: 1}
	ffd, _, _ := receiptTestApp(t, transport, "")
	params, _ := json.Marshal(map[string]any{"commands": []any{map[string]any{"cmd": "createReceipt", "params": receiptTestParams()}}})
	req.Command, req.Params = "executeOperation", params
	requireError(t, ffd.Execute(context.Background(), req), 400, "invalid_request")
	if transport.opens != 0 {
		t.Fatal("nested receipt operation reached hardware")
	}
}

func TestCreateReceiptUnknownShiftStateSendsNoMutation(t *testing.T) {
	transport := &receiptTestTransport{state: 2}
	app, _, _ := receiptTestApp(t, transport, "")
	reply := app.Execute(context.Background(), receiptTestRequest(t, receiptTestParams()))
	if reply.HTTPStatus < 400 || responseBody(t, reply).Status == "unknown" {
		t.Fatalf("unknown shift state must be a definite pre-mutation failure: %s", reply.Body)
	}
	if got := receiptCalls(transport); !bytes.Equal(got, []byte{0x02, 0x20}) {
		t.Fatalf("unknown shift state sent mutating commands: %s", hex.EncodeToString(got))
	}
}

func TestCreateReceiptMultipleItemsAndOptionalData(t *testing.T) {
	transport := &receiptTestTransport{state: 1}
	app, _, _ := receiptTestApp(t, transport, "")
	params := receiptTestParams()
	delete(params, "additionalData")
	delete(params, "payment")
	params["items"] = []any{
		map[string]any{"name": "First", "priceKopecks": 100, "quantity": "1", "vatRate": 11, "paymentMethod": 4, "paymentObject": 1},
		map[string]any{"name": "Second", "priceKopecks": 100, "quantity": "1", "vatRate": 11, "paymentMethod": 4, "paymentObject": 1},
	}
	params["totalKopecks"] = 200
	reply := app.Execute(context.Background(), receiptTestRequest(t, params))
	if reply.HTTPStatus != 200 || !bytes.Equal(receiptCalls(transport), []byte{0x02, 0x20, 0x23, 0x96, 0x96, 0x24}) {
		t.Fatalf("multiple item or optional-data sequence failed: calls=%X body=%s", receiptCalls(transport), reply.Body)
	}
	var names []string
	for _, call := range transport.calls {
		if call.Code != 0x96 {
			continue
		}
		values, err := ParseTLV(call.Payload)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			if value.Tag == 1030 {
				names = append(names, string(value.Value))
			}
		}
	}
	if len(names) != 2 || names[0] != "First" || names[1] != "Second" {
		t.Fatalf("item order or flat FFD TLV parameters were lost: %v", names)
	}
}

func TestCreateReceiptCleanupRejectsChangedPhysicalIdentity(t *testing.T) {
	transport := &receiptTestTransport{state: 1}
	transport.respond = func(_ context.Context, call receiptWireCall) ([]byte, error) {
		if call.Code == 0x96 {
			return nil, &protocol.DeviceError{Code: 0x28}
		}
		if call.Code == 0x02 && call.Session == 2 {
			return []byte("550101046183"), nil
		}
		return receiptTestResponse(call.Code, 1), nil
	}
	app, _, _ := receiptTestApp(t, transport, "")
	reply := app.Execute(context.Background(), receiptTestRequest(t, receiptTestParams()))
	requireError(t, reply, 409, "outcome_unknown")
	cleanup := requireReceiptCleanup(t, reply, true, false)
	problem, ok := cleanup["error"].(map[string]any)
	if !ok || problem["code"] != "device_identity_mismatch" {
		t.Fatalf("cleanup identity failure was not reported: %s", reply.Body)
	}
	if got := receiptCalls(transport); !bytes.Equal(got, []byte{0x02, 0x20, 0x23, 0x96, 0x02}) {
		t.Fatalf("cleanup canceled a different physical KKT: %X", got)
	}
}

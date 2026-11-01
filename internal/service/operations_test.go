package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	terminalfa "github.com/dronm/goterminalfa"
	"github.com/dronm/goterminalfa/internal/protocol"
	"github.com/dronm/goterminalfa/internal/store"
)

func operationTestCommand(code byte, name string, readOnly bool, aliases ...string) Command {
	return Command{
		Definition: CommandDefinition{Name: name, ProtocolCode: fmt.Sprintf("0x%02X", code), Aliases: aliases,
			ReadOnly: readOnly, IdempotencyRequired: !readOnly, Implemented: true},
		Validate: func(raw json.RawMessage) error {
			_, err := operationObject(raw)
			return err
		},
		Execute: func(ctx context.Context, d *Device, _ json.RawMessage) (any, error) {
			data, err := d.Client.Call(ctx, code, nil)
			if err != nil {
				return nil, err
			}
			return map[string]any{"rawHex": hex.EncodeToString(data)}, nil
		},
	}
}

func operationTestCommands() []Command {
	return []Command{
		operationTestCommand(0x23, "beginReceipt", false, "BEGIN"),
		operationTestCommand(0x24, "completeReceipt", false, "END"),
		operationTestCommand(0x2B, "sendItem", false),
		operationTestCommand(0x25, "beginCorrection", false),
		operationTestCommand(0x26, "completeCorrection", false),
		operationTestCommand(0x35, "selectDocument", false),
		operationTestCommand(0x36, "readDocumentTLV", false),
		operationTestCommand(0x64, "appendPrintBuffer", false),
		operationTestCommand(0x65, "printBuffer", false),
		operationTestCommand(0x08, "readStatus", true),
		operationTestCommand(0x12, "startRegistration", false),
		operationTestCommand(0x16, "sendRegistrationData", false),
		operationTestCommand(0x13, "completeRegistration", false),
	}
}

func operationRequest(raw string) Request {
	return Request{DeviceID: "kkt-1", Command: "executeOperation", RequestID: "operation-1", Params: json.RawMessage(raw)}
}

func TestOperationRejectsIncompleteAndAmbiguousSequences(t *testing.T) {
	command := operationCommand(operationTestCommands())
	cases := []string{
		`{}`,
		`{"commands":[]}`,
		`{"commands":null}`,
		`{"commands":[{"cmd":"executeOperation"}]}`,
		`{"commands":[{"cmd":"notImplemented"}]}`,
		`{"commands":[{"cmd":"beginReceipt"}]}`,
		`{"commands":[{"cmd":"sendItem"}]}`,
		`{"commands":[{"cmd":"beginReceipt"},{"cmd":"completeCorrection"}]}`,
		`{"commands":[{"cmd":"beginReceipt"},{"cmd":"beginCorrection"},{"cmd":"completeCorrection"},{"cmd":"completeReceipt"}]}`,
		`{"commands":[{"cmd":"appendPrintBuffer"}]}`,
		`{"commands":[{"cmd":"selectDocument"}]}`,
		`{"commands":[{"cmd":"readDocumentTLV"}]}`,
		`{"commands":[{"cmd":"readStatus","extra":true}]}`,
		`{"commands":[{"Cmd":"readStatus"}]}`,
		`{"commands":[{"cmd":"readStatus","repeatUntilComplete":true}]}`,
		`{"commands":[{"cmd":"readStatus","repeatUntilComplete":false}]}`,
		`{"commands":[{"cmd":"selectDocument"},{"cmd":"readDocumentTLV","repeatUntilComplete":null}]}`,
		`{"commands":[{"cmd":"readStatus","params":{"a":1,"a":2}}]}`,
		`{"commands":[{"cmd":"readStatus","cmd":"beginReceipt"}]}`,
		`{"commands":[{"cmd":"readStatus","params":null}]}`,
		`{"commands":[{"cmd":"readStatus"}],"extra":1}`,
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			if err := command.Validate(json.RawMessage(raw)); err == nil {
				t.Fatal("invalid operation was accepted")
			}
		})
	}
	large := make([]map[string]string, maxOperationCommands+1)
	for i := range large {
		large[i] = map[string]string{"cmd": "readStatus"}
	}
	raw, _ := json.Marshal(map[string]any{"commands": large})
	if err := command.Validate(raw); err == nil {
		t.Fatal("accepted an unbounded operation")
	}
}

func TestOperationRegistrationReasonMatchesReportType(t *testing.T) {
	command := operationCommand(operationTestCommands())
	cases := []struct {
		raw   string
		valid bool
	}{
		{`{"commands":[{"cmd":"startRegistration","params":{"reportType":0}},{"cmd":"sendRegistrationData"},{"cmd":"completeRegistration"}]}`, true},
		{`{"commands":[{"cmd":"startRegistration","params":{"reportType":0}},{"cmd":"completeRegistration","params":{"reregistrationReason":1}}]}`, false},
		{`{"commands":[{"cmd":"startRegistration","params":{"reportType":1}},{"cmd":"completeRegistration"}]}`, false},
		{`{"commands":[{"cmd":"startRegistration","params":{"reportType":2}},{"cmd":"completeRegistration","params":{"reregistrationReason":3}}]}`, true},
	}
	for _, tc := range cases {
		if err := command.Validate(json.RawMessage(tc.raw)); (err == nil) != tc.valid {
			t.Fatalf("valid=%v error=%v request=%s", tc.valid, err, tc.raw)
		}
	}
}

type operationSessionExchange struct {
	mu      sync.Mutex
	calls   []byte
	opens   int
	closes  int
	active  bool
	execute func(context.Context, byte, []byte) ([]byte, error)
}

func (x *operationSessionExchange) Exchange(ctx context.Context, code byte, payload []byte) ([]byte, error) {
	x.mu.Lock()
	x.calls = append(x.calls, code)
	x.mu.Unlock()
	if x.execute != nil {
		return x.execute(ctx, code, payload)
	}
	return nil, nil
}

func (x *operationSessionExchange) WithSession(ctx context.Context, callback func(terminalfa.Exchanger) error) error {
	x.mu.Lock()
	x.opens++
	x.active = true
	x.mu.Unlock()
	defer func() {
		x.mu.Lock()
		x.closes++
		x.active = false
		x.mu.Unlock()
	}()
	return callback(x)
}

func TestOperationUsesOneSessionAndReplaysCanonicalAliases(t *testing.T) {
	transport := &operationSessionExchange{}
	device := NewDevice("kkt-1", "serial", "", terminalfa.NewClient(transport), 2)
	commands := operationTestCommands()
	commands = append(commands, operationCommand(commands))
	app, journal := testService(t, []*Device{device}, commands)
	req := operationRequest(`{"commands":[{"cmd":"BEGIN"},{"cmd":"sendItem","params":{"b":2,"a":1}},{"cmd":"END"}]}`)
	first := app.Execute(context.Background(), req)
	if first.HTTPStatus != 200 {
		t.Fatalf("operation failed: %s", first.Body)
	}
	if !bytes.Equal(transport.calls, []byte{0x23, 0x2B, 0x24}) || transport.opens != 1 || transport.closes != 1 {
		t.Fatalf("wrong session lifecycle: calls=%X opens=%d closes=%d", transport.calls, transport.opens, transport.closes)
	}
	req.Params = json.RawMessage(`{"commands":[{"cmd":"beginReceipt","params":{}},{"cmd":"sendItem","params":{"a":1,"b":2}},{"cmd":"completeReceipt"}]}`)
	replay := app.Execute(context.Background(), req)
	if !replay.Replayed || !bytes.Equal(replay.Body, first.Body) || transport.opens != 1 {
		t.Fatalf("canonical aliases were re-executed: %s", replay.Body)
	}
	record, err := journal.Get(context.Background(), req.DeviceID, req.RequestID)
	if err != nil || record.Command != "executeOperation" || bytes.Contains(record.Params, []byte("BEGIN")) || bytes.Contains(record.Params, []byte("END")) {
		t.Fatalf("canonical operation not persisted: %+v %v", record, err)
	}
	req.RequestID = ""
	requireError(t, app.Execute(context.Background(), req), 400, "invalid_request")
}

func TestOperationCommandsCannotRunSeparately(t *testing.T) {
	device := unusedDevice(t, "kkt-1")
	commands := operationTestCommands()
	app, _ := testService(t, []*Device{device}, commands)
	for _, command := range commands {
		if !isWorkflowCommand(command.Definition.ProtocolCode) {
			continue
		}
		reply := app.Execute(context.Background(), Request{DeviceID: device.ID, Command: command.Definition.Name, RequestID: "not-sent"})
		requireError(t, reply, 400, "operation_required")
	}
}

func TestOperationCannotInterleaveWithAnotherDeviceRequest(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	transport := &operationSessionExchange{execute: func(ctx context.Context, code byte, _ []byte) ([]byte, error) {
		if code == 0x23 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return nil, nil
	}}
	device := NewDevice("kkt-1", "serial", "", terminalfa.NewClient(transport), 2)
	commands := operationTestCommands()
	commands = append(commands, operationCommand(commands))
	app, _ := testService(t, []*Device{device}, commands)
	first := make(chan Reply, 1)
	go func() {
		first <- app.Execute(context.Background(), operationRequest(`{"commands":[{"cmd":"beginReceipt"},{"cmd":"completeReceipt"}]}`))
	}()
	awaitSignal(t, entered)
	second := make(chan Reply, 1)
	go func() {
		second <- app.Execute(context.Background(), Request{DeviceID: device.ID, Command: "readStatus"})
	}()
	awaitSlots(t, device, 2)
	close(release)
	if reply := awaitReply(t, first); reply.HTTPStatus != 200 {
		t.Fatalf("batch failed: %s", reply.Body)
	}
	if reply := awaitReply(t, second); reply.HTTPStatus != 200 {
		t.Fatalf("queued query failed: %s", reply.Body)
	}
	if !bytes.Equal(transport.calls, []byte{0x23, 0x24, 0x08}) {
		t.Fatalf("requests interleaved: %X", transport.calls)
	}
}

func TestOperationLaterRejectionRetainsUnknownOutcome(t *testing.T) {
	calls := 0
	transport := &operationSessionExchange{execute: func(context.Context, byte, []byte) ([]byte, error) {
		calls++
		if calls == 2 {
			return nil, &protocol.DeviceError{Code: 0x28}
		}
		return nil, nil
	}}
	device := NewDevice("kkt-1", "serial", "", terminalfa.NewClient(transport), 2)
	commands := operationTestCommands()
	commands = append(commands, operationCommand(commands))
	app, journal := testService(t, []*Device{device}, commands)
	req := operationRequest(`{"commands":[{"cmd":"beginReceipt"},{"cmd":"completeReceipt"}]}`)
	requireError(t, app.Execute(context.Background(), req), 409, "outcome_unknown")
	record, err := journal.Get(context.Background(), req.DeviceID, req.RequestID)
	if err != nil || record.State != store.StateUnknown {
		t.Fatalf("partially applied operation not retained: %+v %v", record, err)
	}
	requireError(t, app.Execute(context.Background(), req), 409, "outcome_unknown")
	if calls != 2 {
		t.Fatalf("partially applied operation was retried: %d calls", calls)
	}
}

func TestOperationFirstRejectionIsDefinite(t *testing.T) {
	transport := &operationSessionExchange{execute: func(context.Context, byte, []byte) ([]byte, error) {
		return nil, &protocol.DeviceError{Code: 0x28}
	}}
	device := NewDevice("kkt-1", "serial", "", terminalfa.NewClient(transport), 2)
	commands := operationTestCommands()
	commands = append(commands, operationCommand(commands))
	app, _ := testService(t, []*Device{device}, commands)
	req := operationRequest(`{"commands":[{"cmd":"beginReceipt"},{"cmd":"completeReceipt"}]}`)
	first := app.Execute(context.Background(), req)
	requireError(t, first, 502, "device_error")
	second := app.Execute(context.Background(), req)
	if !second.Replayed || !bytes.Equal(first.Body, second.Body) || len(transport.calls) != 1 {
		t.Fatalf("definite rejection was retried: %s", second.Body)
	}
}

func TestOperationArchiveExplicitDrainIsBoundedAndComplete(t *testing.T) {
	cases := []struct {
		name     string
		expected uint16
		chunk    []byte
		steps    string
		wantOK   bool
		reads    int
	}{
		{"drained", 8, []byte{1, 2, 3, 4}, `{"cmd":"readDocumentTLV","repeatUntilComplete":true}`, true, 2},
		{"exact-static", 8, []byte{1, 2, 3, 4}, `{"cmd":"readDocumentTLV"},{"cmd":"readDocumentTLV"}`, true, 2},
		{"incomplete", 8, []byte{1, 2, 3, 4}, `{"cmd":"readDocumentTLV"}`, false, 1},
		{"no-progress", 8, nil, `{"cmd":"readDocumentTLV","repeatUntilComplete":true}`, false, 1},
		{"overshoot", 3, []byte{1, 2, 3, 4}, `{"cmd":"readDocumentTLV","repeatUntilComplete":true}`, false, 1},
		{"bounded", maxArchiveReads + 1, []byte{1}, `{"cmd":"readDocumentTLV","repeatUntilComplete":true}`, false, maxArchiveReads},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			transport := &operationSessionExchange{execute: func(_ context.Context, code byte, _ []byte) ([]byte, error) {
				if code == 0x35 {
					data := []byte{1, 0, 0, 0}
					binary.LittleEndian.PutUint16(data[2:], tc.expected)
					return data, nil
				}
				reads++
				return tc.chunk, nil
			}}
			device := NewDevice("kkt-1", "serial", "", terminalfa.NewClient(transport), 2)
			commands := operationTestCommands()
			commands = append(commands, operationCommand(commands))
			app, _ := testService(t, []*Device{device}, commands)
			req := operationRequest(`{"commands":[{"cmd":"selectDocument"},` + tc.steps + `]}`)
			reply := app.Execute(context.Background(), req)
			if tc.wantOK {
				if reply.HTTPStatus != 200 {
					t.Fatalf("archive operation failed: %s", reply.Body)
				}
			} else {
				requireError(t, reply, 409, "outcome_unknown")
			}
			if reads != tc.reads {
				t.Fatalf("read count %d, expected %d", reads, tc.reads)
			}
		})
	}
}

func TestOperationCancellationStopsLaterCommands(t *testing.T) {
	transport := &operationSessionExchange{execute: func(ctx context.Context, code byte, _ []byte) ([]byte, error) {
		if code == 0x23 {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return nil, errors.New("must not reach another step")
	}}
	device := NewDevice("kkt-1", "serial", "", terminalfa.NewClient(transport), 2)
	commands := operationTestCommands()
	commands = append(commands, operationCommand(commands))
	app, _ := testService(t, []*Device{device}, commands)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	req := operationRequest(`{"commands":[{"cmd":"beginReceipt"},{"cmd":"completeReceipt"}]}`)
	requireError(t, app.Execute(ctx, req), 409, "outcome_unknown")
	if len(transport.calls) != 1 {
		t.Fatalf("operation continued after cancellation: %X", transport.calls)
	}
}

func TestOperationIdentityVerificationUsesSameSession(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(fmt.Sprintf("mismatch=%v", mismatch), func(t *testing.T) {
			transport := &operationSessionExchange{}
			transport.execute = func(_ context.Context, code byte, _ []byte) ([]byte, error) {
				transport.mu.Lock()
				active := transport.active
				transport.mu.Unlock()
				if !active {
					t.Error("identity or fiscal command ran outside the operation session")
				}
				if code == 0x02 {
					if mismatch {
						return []byte("550101046183"), nil
					}
					return []byte("550101046182"), nil
				}
				return nil, nil
			}
			device := NewDevice("kkt-1", "serial", "550101046182", terminalfa.NewClient(transport), 2)
			commands := operationTestCommands()
			commands = append(commands, operationCommand(commands))
			app, journal := testService(t, []*Device{device}, commands)
			req := operationRequest(`{"commands":[{"cmd":"beginReceipt"},{"cmd":"completeReceipt"}]}`)
			first := app.Execute(context.Background(), req)
			if mismatch {
				requireError(t, first, 502, "device_identity_mismatch")
				if !bytes.Equal(transport.calls, []byte{0x02}) {
					t.Fatalf("identity mismatch sent fiscal commands: %X", transport.calls)
				}
				record, err := journal.Get(context.Background(), req.DeviceID, req.RequestID)
				if err != nil || record.State != store.StateCompleted {
					t.Fatalf("identity failure is not a definite persisted outcome: %+v %v", record, err)
				}
				replayed := app.Execute(context.Background(), req)
				if !replayed.Replayed || !bytes.Equal(first.Body, replayed.Body) {
					t.Fatalf("definite identity failure was retried: %s", replayed.Body)
				}
			} else {
				if first.HTTPStatus != 200 || !bytes.Equal(transport.calls, []byte{0x02, 0x23, 0x24}) {
					t.Fatalf("identity and mutation did not use the same sequence: calls=%X body=%s", transport.calls, first.Body)
				}
			}
			if transport.opens != 1 || transport.closes != 1 {
				t.Fatalf("identity verification reopened the port: opens=%d closes=%d", transport.opens, transport.closes)
			}
		})
	}
}

func TestOperationReceiptConsumesFormattedBufferWithRealCatalogue(t *testing.T) {
	buffer := map[string]any{"cmd": "bufferFormattedText", "params": map[string]any{
		"lines": []any{map[string]any{"fontAttributes": 0, "alignment": 1, "text": "Receipt note"}},
	}}
	begin := map[string]any{"cmd": "beginReceipt"}
	item := map[string]any{"cmd": "addReceiptItem", "params": map[string]any{"item": map[string]any{
		"name": "Item", "priceKopecks": 100, "quantity": "1", "vatRate": 6, "paymentMethod": 4,
	}}}
	payment := map[string]any{"cmd": "setReceiptPayment", "params": map[string]any{
		"taxSystem": 1, "cashKopecks": 100, "cashlessKopecks": 0, "prepaymentKopecks": 0,
		"creditKopecks": 0, "counterProvisionKopecks": 0,
	}}
	for _, before := range []bool{false, true} {
		for _, additional := range []bool{false, true} {
			t.Run(fmt.Sprintf("before=%v/additionalText=%v", before, additional), func(t *testing.T) {
				finalParams := map[string]any{"calculationSign": 1, "totalKopecks": 100}
				if additional {
					finalParams["additionalText"] = "Override text"
				}
				finalize := map[string]any{"cmd": "finalizeReceipt", "params": finalParams}
				steps := []any{begin, buffer, item, payment, finalize}
				want := []byte{0x23, 0x64, 0x2B, 0x2D, 0x24}
				if before {
					steps = []any{buffer, begin, item, payment, finalize}
					want = []byte{0x64, 0x23, 0x2B, 0x2D, 0x24}
				}
				body, _ := json.Marshal(map[string]any{"commands": steps})
				transport := &operationSessionExchange{execute: func(_ context.Context, code byte, _ []byte) ([]byte, error) {
					if code == 0x24 {
						return []byte{1, 0, 2, 0, 0, 0, 3, 0, 0, 0}, nil
					}
					return nil, nil
				}}
				device := NewDevice("kkt-1", "serial", "", terminalfa.NewClient(transport), 2)
				app, _ := testService(t, []*Device{device}, BuiltinCommands())
				req := operationRequest(string(body))
				reply := app.Execute(context.Background(), req)
				if reply.HTTPStatus != 200 || !bytes.Equal(transport.calls, want) {
					t.Fatalf("buffered receipt failed or wrong commands sent: calls=%X body=%s", transport.calls, reply.Body)
				}
			})
		}
	}
}

func TestOperationCumulativePrinterBufferLimit(t *testing.T) {
	commands := BuiltinCommands()
	var operation Command
	for _, command := range commands {
		if command.Definition.Name == "executeOperation" {
			operation = command
		}
	}
	if operation.Validate == nil {
		t.Fatal("operation command is not registered")
	}
	buffer := func(count int) any {
		lines := make([]any, count)
		for i := range lines {
			lines[i] = map[string]any{"fontAttributes": 0, "alignment": 0, "text": "x"}
		}
		return map[string]any{"cmd": "bufferFormattedText", "params": map[string]any{"lines": lines}}
	}
	for _, total := range []int{255, 256} {
		steps := []any{buffer(100), buffer(100), buffer(total - 200), map[string]any{"cmd": "printTextBuffer"}}
		raw, _ := json.Marshal(map[string]any{"commands": steps})
		err := operation.Validate(raw)
		if (err == nil) != (total == 255) {
			t.Fatalf("cumulative lines=%d error=%v", total, err)
		}
	}
	// Finishing a normal receipt resets the buffer budget for later work.
	steps := []any{buffer(100), buffer(100), buffer(55), map[string]any{"cmd": "beginReceipt"},
		map[string]any{"cmd": "finalizeReceipt", "params": map[string]any{"calculationSign": 1, "totalKopecks": 0}},
		buffer(100), map[string]any{"cmd": "printTextBuffer"}}
	raw, _ := json.Marshal(map[string]any{"commands": steps})
	if err := operation.Validate(raw); err != nil {
		t.Fatalf("receipt completion did not reset buffer capacity: %v", err)
	}
}

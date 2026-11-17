package service

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	terminalfa "github.com/dronm/goterminalfa"
)

func TestFFD12ReportContextResetsOnCancellationAndBetweenRequests(t *testing.T) {
	operation := ffd12OperationDefinition(t)
	for _, test := range []struct {
		commands, finalizer string
	}{
		{`[{"cmd":"0x14"},{"cmd":"0x10"},{"cmd":"0x27"},{"cmd":"0x28"}]`, "finalizeSettlementReport"},
		{`[{"cmd":"0x27"},{"cmd":"0x10"},{"cmd":"0x14"},{"cmd":"0x28"}]`, "finalizeFiscalStorageClosure"},
		{`[{"cmd":"0x27"},{"cmd":"0x28"}]`, "finalizeSettlementReport"},
		{`[{"cmd":"0x14"},{"cmd":"0x28"}]`, "finalizeFiscalStorageClosure"},
	} {
		canonical, err := operation.Canonicalize(json.RawMessage(`{"commands":` + test.commands + `}`))
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Commands []operationStep `json:"commands"`
		}
		if err := json.Unmarshal(canonical, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Commands[len(decoded.Commands)-1].Command != test.finalizer {
			t.Fatalf("report finalizer inherited another context: %s", canonical)
		}
		again, err := operation.Canonicalize(canonical)
		if err != nil || !bytes.Equal(again, canonical) {
			t.Fatalf("canonical report cannot be parsed consistently: first=%s again=%s error=%v", canonical, again, err)
		}
	}
	for _, commands := range []string{
		`[{"cmd":"0x14"},{"cmd":"0x10"},{"cmd":"0x28"}]`,
		`[{"cmd":"0x27"},{"cmd":"0x10"},{"cmd":"0x28"}]`,
		`[{"cmd":"0x28"}]`,
	} {
		if err := operation.Validate(json.RawMessage(`{"commands":` + commands + `}`)); err == nil {
			t.Fatalf("finalizer accepted outside the current operation's active report: %s", commands)
		}
	}
}

func TestFFD12ClosureOwnsOneSessionAndReplaysAcrossServiceRestart(t *testing.T) {
	transport := &operationSessionExchange{execute: func(_ context.Context, code byte, _ []byte) ([]byte, error) {
		if code == 0x28 {
			return []byte{1, 0, 0, 0, 2, 0, 0, 0}, nil
		}
		return nil, nil
	}}
	device := NewDevice("fn-closure", "serial", "", terminalfa.NewClient(transport), 2)
	device.ProtocolProfile = "ffd1.2"
	app, journal := testService(t, []*Device{device}, BuiltinCommands())
	req := Request{
		DeviceID: device.ID, Command: "executeOperation", RequestID: "close-fn-once",
		Params: json.RawMessage(`{"commands":[{"cmd":"0x14"},{"cmd":"0x17","params":{"documentAddress":"A","documentPlace":"P"}},{"cmd":"0x28"}]}`),
	}
	first := app.Execute(context.Background(), req)
	if first.HTTPStatus != 200 || !bytes.Equal(transport.calls, []byte{0x14, 0x17, 0x28}) || transport.opens != 1 || transport.closes != 1 {
		t.Fatalf("closure did not retain one serial session: response=%s calls=% X opens=%d closes=%d", first.Body, transport.calls, transport.opens, transport.closes)
	}
	record, err := journal.Get(context.Background(), device.ID, req.RequestID)
	if err != nil || !bytes.Contains(record.Params, []byte(`"cmd":"finalizeFiscalStorageClosure"`)) || bytes.Contains(record.Params, []byte(`"cmd":"0x28"`)) {
		t.Fatalf("context-selected finalizer was not persisted canonically: params=%s error=%v", record.Params, err)
	}
	restarted := serviceWithJournal(t, []*Device{device}, BuiltinCommands(), journal)
	req.Params = json.RawMessage(`{"commands":[{"cmd":"beginFiscalStorageClosure"},{"cmd":"sendFiscalStorageClosureData","params":{"documentPlace":"P","documentAddress":"A"}},{"cmd":"finalizeFiscalStorageClosure"}]}`)
	replay := restarted.Execute(context.Background(), req)
	if !replay.Replayed || replay.HTTPStatus != 200 || !bytes.Equal(first.Body, replay.Body) || len(transport.calls) != 3 || transport.opens != 1 {
		t.Fatalf("a persisted closure was executed again: response=%s calls=% X opens=%d", replay.Body, transport.calls, transport.opens)
	}
}

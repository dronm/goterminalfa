package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	terminalfa "github.com/dronm/goterminalfa"
)

func ffd12WorkflowSteps(profile string, codes ...byte) []operationStep {
	steps := make([]operationStep, 0, len(codes))
	for _, code := range codes {
		params := json.RawMessage(`{}`)
		if code == 0x98 {
			params = json.RawMessage(`{"correctedCalculationDate":1704067200}`)
		}
		name := fmt.Sprintf("command%02X", code)
		steps = append(steps, operationStep{Command: name, Params: params, resolved: Command{
			Definition: CommandDefinition{Name: name, ProtocolCode: fmt.Sprintf("0x%02X", code), ProtocolProfile: profile},
		}})
	}
	return steps
}

func TestFFD12WorkflowRolesAndBaseCompatibility(t *testing.T) {
	cases := []struct {
		name    string
		profile string
		codes   []byte
		valid   bool
	}{
		{"normal receipt", "ffd1.2", []byte{0x23, 0x96, 0x98, 0x2D, 0x19, 0x24}, true},
		{"correction receipt", "ffd1.2", []byte{0x25, 0x96, 0x98, 0x2D, 0x26}, true},
		{"base normal receipt unchanged", "base", []byte{0x23, 0x2B, 0x2D, 0x24}, true},
		{"base correction unchanged", "base", []byte{0x25, 0x2E, 0x26}, true},
		{"base correction cannot use payment command", "base", []byte{0x25, 0x2D, 0x26}, false},
		{"cashless details prohibited for correction", "ffd1.2", []byte{0x25, 0x96, 0x98, 0x2D, 0x19, 0x26}, false},
		{"cashless details need preceding payment", "ffd1.2", []byte{0x23, 0x19, 0x2D, 0x24}, false},
		{"base cashless workflow is not tightened", "base", []byte{0x23, 0x19, 0x24}, true},
		{"payment state resets between receipts", "ffd1.2", []byte{0x23, 0x2D, 0x24, 0x23, 0x19, 0x24}, false},
		{"normal additional data may follow payment", "ffd1.2", []byte{0x23, 0x2D, 0x98, 0x24}, true},
		{"correction additional data may follow payment", "ffd1.2", []byte{0x25, 0x2D, 0x98, 0x26}, true},
		{"correction needs dated additional data", "ffd1.2", []byte{0x25, 0x96, 0x2D, 0x26}, false},
		{"correction cannot skip additional data", "ffd1.2", []byte{0x25, 0x26}, false},
		{"shift opening data", "ffd1.2", []byte{0x21, 0xE4, 0x2F, 0x22}, true},
		{"shift closing data", "ffd1.2", []byte{0x29, 0xE5, 0x2F, 0x2A}, true},
		{"settlement report data", "ffd1.2", []byte{0x27, 0xE6, 0x28}, true},
		{"opening data in closing workflow", "ffd1.2", []byte{0x29, 0xE4, 0x2A}, false},
		{"closing data in opening workflow", "ffd1.2", []byte{0x21, 0xE5, 0x22}, false},
		{"report data in shift workflow", "ffd1.2", []byte{0x21, 0xE6, 0x22}, false},
		{"item outside receipt", "ffd1.2", []byte{0x96}, false},
		{"additional data outside receipt", "ffd1.2", []byte{0x98}, false},
		{"opening data outside workflow", "ffd1.2", []byte{0xE4}, false},
		{"closing data outside workflow", "ffd1.2", []byte{0xE5}, false},
		{"report data outside workflow", "ffd1.2", []byte{0xE6}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOperationWorkflow(ffd12WorkflowSteps(tc.profile, tc.codes...))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, workflow=%X, error=%v", tc.valid, tc.codes, err)
			}
		})
	}
	for _, code := range []string{"0x96", "0x98", "0xE4", "0xE5", "0xE6"} {
		if !isWorkflowCommand(code) {
			t.Fatalf("%s is not guarded as an operation-only command", code)
		}
	}
	// A caller cannot opt into FFD correction payment handling merely by
	// putting a base-profile 0x2D into an otherwise FFD operation.
	mixed := ffd12WorkflowSteps("ffd1.2", 0x25, 0x98, 0x2D, 0x26)
	mixed[2].resolved.Definition.ProtocolProfile = "base"
	if err := validateOperationWorkflow(mixed); err == nil {
		t.Fatal("base-profile 0x2D accepted inside a correction receipt")
	}
}

func TestFFD12CorrectionDateIsAnOperationRequirement(t *testing.T) {
	for _, params := range []string{`{}`, `{"cashierName":"Cashier"}`} {
		steps := ffd12WorkflowSteps("ffd1.2", 0x25, 0x98, 0x2D, 0x26)
		steps[1].Params = json.RawMessage(params)
		if err := validateOperationWorkflow(steps); err == nil {
			t.Fatalf("correction accepted without its corrected date: %s", params)
		}
	}
	// Separate optional 0x98 calls may add more fields, provided one of them
	// supplies the correction date before finalization. No ordering relative
	// to the payment-data command is specified by the supplement.
	steps := ffd12WorkflowSteps("ffd1.2", 0x25, 0x98, 0x2D, 0x98, 0x26)
	steps[1].Params = json.RawMessage(`{"cashierName":"Cashier"}`)
	if err := validateOperationWorkflow(steps); err != nil {
		t.Fatalf("correction with an explicit date in a later 0x98 was rejected: %v", err)
	}
}

func ffd12OperationDefinition(t *testing.T) Command {
	t.Helper()
	commands, err := BuiltinCommandsForProfile("ffd1.2")
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if command.Definition.Name == "executeOperation" {
			return command
		}
	}
	t.Fatal("FFD 1.2 operation command not found")
	return Command{}
}

func TestFFD12RealCatalogueWorkflowsValidate(t *testing.T) {
	op := ffd12OperationDefinition(t)
	if op.Definition.Source.Version != "0.6.0" {
		t.Fatalf("operation metadata version=%q", op.Definition.Source.Version)
	}
	for _, raw := range []string{
		`{"commands":[{"cmd":"0x23"},{"cmd":"0x96","params":{"name":"A","priceKopecks":100,"quantity":"1","vatRate":11,"paymentMethod":4,"paymentObject":1}},{"cmd":"0x98","params":{"cashierName":"Cashier"}},{"cmd":"0x2D","params":{"taxSystem":1,"cashKopecks":100,"cashlessKopecks":0,"prepaymentKopecks":0,"creditKopecks":0,"counterProvisionKopecks":0}},{"cmd":"0x24","params":{"calculationSign":1,"totalKopecks":100}}]}`,
		`{"commands":[{"cmd":"0x25"},{"cmd":"0x96","params":{"name":"A","priceKopecks":100,"quantity":"1","vatRate":11,"paymentMethod":4,"paymentObject":1}},{"cmd":"0x98","params":{"correctedCalculationDate":1704067200}},{"cmd":"0x2D","params":{"taxSystem":1,"cashKopecks":100,"cashlessKopecks":0,"prepaymentKopecks":0,"creditKopecks":0,"counterProvisionKopecks":0}},{"cmd":"0x26","params":{"calculationSign":1,"totalKopecks":100}}]}`,
		`{"commands":[{"cmd":"0x23"},{"cmd":"0x2D","params":{"taxSystem":1,"cashKopecks":0,"cashlessKopecks":0,"prepaymentKopecks":0,"creditKopecks":0,"counterProvisionKopecks":0}},{"cmd":"0x98","params":{"cashierName":"Cashier"}},{"cmd":"0x24","params":{"calculationSign":1,"totalKopecks":0}}]}`,
		`{"commands":[{"cmd":"0x25"},{"cmd":"0x2D","params":{"taxSystem":1,"cashKopecks":0,"cashlessKopecks":0,"prepaymentKopecks":0,"creditKopecks":0,"counterProvisionKopecks":0}},{"cmd":"0x98","params":{"correctedCalculationDate":1704067200}},{"cmd":"0x26","params":{"calculationSign":1,"totalKopecks":0}}]}`,
		`{"commands":[{"cmd":"0x21","params":{"suppressPrint":1}},{"cmd":"0xE4","params":{"cashierName":"Cashier","documentAddress":"Address","documentPlace":"Place"}},{"cmd":"0x2F","params":{"cashierName":"Cashier"}},{"cmd":"0x22"}]}`,
		`{"commands":[{"cmd":"0x29"},{"cmd":"0xE5","params":{"cashierName":"Cashier","documentAddress":"Address","documentPlace":"Place"}},{"cmd":"0x2F","params":{"cashierName":"Cashier"}},{"cmd":"0x2A"}]}`,
		`{"commands":[{"cmd":"0x27"},{"cmd":"0xE6","params":{"documentAddress":"Address","documentPlace":"Place"}},{"cmd":"0x28"}]}`,
	} {
		if err := op.Validate(json.RawMessage(raw)); err != nil {
			t.Fatalf("real FFD catalogue workflow rejected: %v\n%s", err, raw)
		}
	}
	for _, raw := range []string{
		`{"commands":[{"cmd":"0x96","params":{"name":"A","priceKopecks":100,"quantity":"1","vatRate":11,"paymentMethod":4,"paymentObject":1}}]}`,
		`{"commands":[{"cmd":"0x25"},{"cmd":"0x98","params":{"cashierName":"Cashier"}},{"cmd":"0x2D","params":{"taxSystem":1,"cashKopecks":0,"cashlessKopecks":0,"prepaymentKopecks":0,"creditKopecks":0,"counterProvisionKopecks":0}},{"cmd":"0x26","params":{"calculationSign":1,"totalKopecks":0}}]}`,
		`{"commands":[{"cmd":"0x29"},{"cmd":"0xE4","params":{"cashierName":"Cashier","documentAddress":"Address","documentPlace":"Place"}},{"cmd":"0x2A"}]}`,
	} {
		if err := op.Validate(json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid real FFD workflow accepted: %s", raw)
		}
	}
}

func TestFFD12CorrectionOperationUsesProfileAndReplays(t *testing.T) {
	transport := &operationSessionExchange{execute: func(_ context.Context, code byte, _ []byte) ([]byte, error) {
		if code == 0x26 {
			return []byte{1, 0, 2, 0, 0, 0, 3, 0, 0, 0, 26, 11, 2, 9, 0, 4, 0}, nil
		}
		return nil, nil
	}}
	device := NewDevice("kkt-1", "serial", "", terminalfa.NewClient(transport), 2)
	device.ProtocolProfile = "ffd1.2"
	app, _ := testService(t, []*Device{device}, BuiltinCommands())
	req := operationRequest(`{"commands":[{"cmd":"0x25"},{"cmd":"0x96","params":{"name":"A","priceKopecks":100,"quantity":"1","vatRate":11,"paymentMethod":4,"paymentObject":1}},{"cmd":"0x2D","params":{"taxSystem":1,"cashKopecks":100,"cashlessKopecks":0,"prepaymentKopecks":0,"creditKopecks":0,"counterProvisionKopecks":0}},{"cmd":"0x98","params":{"correctedCalculationDate":1704067200}},{"cmd":"0x26","params":{"calculationSign":1,"totalKopecks":100}}]}`)
	first := app.Execute(context.Background(), req)
	if first.HTTPStatus != 200 || !bytes.Equal(transport.calls, []byte{0x25, 0x96, 0x2D, 0x98, 0x26}) {
		t.Fatalf("FFD correction execution failed: calls=%X body=%s", transport.calls, first.Body)
	}
	second := app.Execute(context.Background(), req)
	if !second.Replayed || !bytes.Equal(first.Body, second.Body) || len(transport.calls) != 5 {
		t.Fatalf("FFD correction was not durably replayed: %s", second.Body)
	}
	if transport.opens != 1 || transport.closes != 1 {
		t.Fatalf("FFD correction did not share one serial session: opens=%d closes=%d", transport.opens, transport.closes)
	}
	for _, name := range []string{"0x96", "0x98", "0xE4", "0xE5", "0xE6"} {
		params := `{}`
		switch name {
		case "0x96":
			params = `{"name":"A","priceKopecks":100,"quantity":"1","vatRate":11,"paymentMethod":4,"paymentObject":1}`
		case "0xE4", "0xE5":
			params = `{"cashierName":"Cashier","documentAddress":"Address","documentPlace":"Place"}`
		case "0xE6":
			params = `{"documentAddress":"Address","documentPlace":"Place"}`
		}
		requireError(t, app.Execute(context.Background(), Request{DeviceID: device.ID, Command: name, RequestID: "standalone", Params: json.RawMessage(params)}), 400, "operation_required")
	}
	if len(transport.calls) != 5 {
		t.Fatal("standalone FFD workflow commands reached the device")
	}
}

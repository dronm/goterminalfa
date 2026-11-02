package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	terminalfa "github.com/dronm/goterminalfa"
)

type ffd12ShiftExchange func(context.Context, byte, []byte) ([]byte, error)

func (f ffd12ShiftExchange) Exchange(ctx context.Context, code byte, data []byte) ([]byte, error) {
	return f(ctx, code, data)
}

func ffd12ShiftFind(t *testing.T, commands []Command, code byte) Command {
	t.Helper()
	for _, command := range commands {
		if command.Definition.ProtocolCode == fmt.Sprintf("0x%02X", code) {
			return command
		}
	}
	t.Fatalf("missing shift command 0x%02X", code)
	return Command{}
}

func ffd12ShiftExecute(t *testing.T, command Command, params string, response []byte) (any, []byte, error) {
	t.Helper()
	if err := command.Validate(json.RawMessage(params)); err != nil {
		return nil, nil, err
	}
	var sent []byte
	device := &Device{Client: terminalfa.NewClient(ffd12ShiftExchange(func(_ context.Context, code byte, data []byte) ([]byte, error) {
		if got := fmt.Sprintf("0x%02X", code); got != command.Definition.ProtocolCode {
			t.Errorf("wire command %s, want %s", got, command.Definition.ProtocolCode)
		}
		sent = append([]byte{}, data...)
		return response, nil
	}))}
	result, err := command.Execute(context.Background(), device, json.RawMessage(params))
	return result, sent, err
}

func ffd12ShiftObject(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func TestFFD12ShiftCataloguePreservesBaseAndCashierCompatibility(t *testing.T) {
	base := registrationCommands()
	before := make([]CommandDefinition, 0, len(base))
	for _, command := range base {
		before = append(before, command.Definition)
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	ffd := ffd12ShiftCommands(base)
	if len(ffd) != 8 {
		t.Fatalf("got %d shift definitions, want 8", len(ffd))
	}
	for _, code := range []byte{0x20, 0x21, 0x22, 0x29, 0x2A, 0x2F, 0xE4, 0xE5} {
		command := ffd12ShiftFind(t, ffd, code)
		if command.Definition.ReadOnly != (code == 0x20) || command.Definition.IdempotencyRequired != (code != 0x20) {
			t.Errorf("wrong policy for %s", command.Definition.ProtocolCode)
		}
		if command.Definition.OperationRequired != (code != 0x20) {
			t.Errorf("wrong workflow policy for %s", command.Definition.ProtocolCode)
		}
		if code != 0x2F {
			if command.Definition.Source.Version != "FFD 1.2" || command.Definition.Source.Page < 9 || command.Definition.Source.Page > 11 || command.Definition.DescriptionRU == "" {
				t.Errorf("wrong supplement source for %s: %+v", command.Definition.ProtocolCode, command.Definition.Source)
			}
		}
	}
	cashierBase := ffd12ShiftFind(t, base, 0x2F)
	cashierFFD := ffd12ShiftFind(t, ffd, 0x2F)
	if !reflect.DeepEqual(cashierBase.Definition.Input, cashierFFD.Definition.Input) || cashierBase.Definition.Source != cashierFFD.Definition.Source {
		t.Fatal("the supplement must not invent a new 0x2F layout or source")
	}
	after := make([]CommandDefinition, 0, len(base))
	for _, command := range base {
		after = append(after, command.Definition)
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeJSON, afterJSON) {
		t.Fatal("constructing FFD shift overrides modified the base catalogue")
	}
	if len(ffd12ShiftCommands(nil)) != 8 {
		t.Fatal("the base-compatible cashier fallback is missing")
	}
}

func TestFFD12ShiftParameterQueryKeepsFiveByteLayout(t *testing.T) {
	command := ffd12ShiftFind(t, ffd12ShiftCommands(registrationCommands()), 0x20)
	result, sent, err := ffd12ShiftExecute(t, command, `{}`, []byte{1, 0x34, 0x12, 0x78, 0x56})
	if err != nil || len(sent) != 0 {
		t.Fatalf("result %#v, sent % X, error %v", result, sent, err)
	}
	want := map[string]any{"shiftState": float64(1), "shiftNumber": float64(0x1234), "receiptNumber": float64(0x5678)}
	if got := ffd12ShiftObject(t, result); !reflect.DeepEqual(got, want) {
		t.Fatalf("shift parameters = %#v, want %#v", got, want)
	}
	for _, response := range [][]byte{make([]byte, 4), make([]byte, 6)} {
		if _, _, err := ffd12ShiftExecute(t, command, `{}`, response); err == nil {
			t.Errorf("accepted malformed query response % X", response)
		}
	}
}

func TestFFD12ShiftCompletionRequiresEleventhWarningByte(t *testing.T) {
	base := registrationCommands()
	ffd := ffd12ShiftCommands(base)
	response := []byte{0x34, 0x12, 4, 3, 2, 1, 0x44, 0x33, 0x22, 0x11, 0x81}
	want := map[string]any{"shiftNumber": float64(0x1234), "documentNumber": float64(0x01020304), "fiscalSign": float64(0x11223344), "warningFlags": float64(0x81)}
	for _, code := range []byte{0x22, 0x2A} {
		command := ffd12ShiftFind(t, ffd, code)
		result, sent, err := ffd12ShiftExecute(t, command, `{}`, response)
		if err != nil || len(sent) != 0 {
			t.Fatalf("0x%02X result %#v, sent % X, error %v", code, result, sent, err)
		}
		if got := ffd12ShiftObject(t, result); !reflect.DeepEqual(got, want) {
			t.Fatalf("0x%02X result %#v, want %#v", code, got, want)
		}
		for _, malformed := range [][]byte{response[:10], append(append([]byte{}, response...), 0)} {
			if _, _, err := ffd12ShiftExecute(t, command, `{}`, malformed); err == nil {
				t.Errorf("0x%02X accepted malformed FFD 1.2 response % X", code, malformed)
			}
		}
		baseCommand := ffd12ShiftFind(t, base, code)
		if _, _, err := ffd12ShiftExecute(t, baseCommand, `{}`, response[:10]); err != nil {
			t.Fatalf("base 0x%02X no longer accepts its ten-byte response: %v", code, err)
		}
		if _, _, err := ffd12ShiftExecute(t, baseCommand, `{}`, response); err == nil {
			t.Fatalf("base 0x%02X unexpectedly consumed an FFD 1.2 warning byte", code)
		}
		warnings := command.Definition.Output[len(command.Definition.Output)-1]
		if warnings.Name != "warningFlags" || len(warnings.Bits) != 8 || warnings.Bits["7"] != "КРИТИЧЕСКАЯ ОШИБКА ФН" {
			t.Fatalf("0x%02X warning dictionary incomplete: %+v", code, warnings)
		}
	}
}

func TestFFD12ShiftPrintSuppressionPreservesOmission(t *testing.T) {
	commands := ffd12ShiftCommands(registrationCommands())
	for _, code := range []byte{0x21, 0x29} {
		command := ffd12ShiftFind(t, commands, code)
		for _, tc := range []struct {
			params string
			wire   []byte
		}{{`{}`, nil}, {`{"suppressPrint":0}`, []byte{0}}, {`{"suppressPrint":1}`, []byte{1}}} {
			_, sent, err := ffd12ShiftExecute(t, command, tc.params, nil)
			if err != nil || !bytes.Equal(sent, tc.wire) {
				t.Fatalf("0x%02X %s encoded % X, want % X, error %v", code, tc.params, sent, tc.wire, err)
			}
		}
		for _, invalid := range []string{`{"suppressPrint":2}`, `{"suppressPrint":-1}`, `{"suppressPrint":true}`, `{"suppressPrint":"1"}`, `{"suppressPrint":null}`} {
			if err := command.Validate(json.RawMessage(invalid)); err == nil {
				t.Errorf("0x%02X accepted %s", code, invalid)
			}
		}
	}
	opening := ffd12ShiftFind(t, commands, 0x21)
	if !strings.Contains(opening.Definition.Description, "fifteen seconds") {
		t.Fatal("marking-key update timing was lost from opening documentation")
	}
}

func TestFFD12ShiftDataUsesExactTLVTagsAndBinaryBytes(t *testing.T) {
	commands := ffd12ShiftCommands(registrationCommands())
	params := `{"cashierName":"A","cashierInn":"123456789012","documentAddress":"B","documentPlace":"C","reportAttribute":"R","reportDataHex":"00ff"}`
	for _, tc := range []struct {
		code byte
		want string
	}{
		{0xE4, "fd03010041b3040c00313233343536373839303132f103010042a304010043fc04010052fd04020000ff"},
		{0xE5, "fd03010041b3040c00313233343536373839303132f103010042a304010043fe04010052ff04020000ff"},
	} {
		command := ffd12ShiftFind(t, commands, tc.code)
		result, sent, err := ffd12ShiftExecute(t, command, params, nil)
		if err != nil || hex.EncodeToString(sent) != tc.want || len(ffd12ShiftObject(t, result)) != 0 {
			t.Fatalf("0x%02X sent % X, result %#v, error %v", tc.code, sent, result, err)
		}
		minimal := `{"cashierName":"A","documentAddress":"B","documentPlace":"C"}`
		_, sent, err = ffd12ShiftExecute(t, command, minimal, nil)
		if err != nil || hex.EncodeToString(sent) != "fd03010041f103010042a304010043" {
			t.Fatalf("0x%02X minimal sent % X, error %v", tc.code, sent, err)
		}
	}
}

func TestFFD12ShiftDataEnforcesEncodedLengthBoundaries(t *testing.T) {
	for _, code := range []byte{0xE4, 0xE5} {
		command := ffd12ShiftFind(t, ffd12ShiftCommands(registrationCommands()), code)
		fields := map[string]any{
			"cashierName": strings.Repeat("Я", 64), "cashierInn": "123456789012",
			"documentAddress": strings.Repeat("Я", 256), "documentPlace": strings.Repeat("Я", 256),
			"reportAttribute": strings.Repeat("Я", 32), "reportDataHex": strings.Repeat("ab", 32),
		}
		body, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if err := command.Validate(body); err != nil {
			t.Fatalf("0x%02X rejected exact CP866/binary limits: %v", code, err)
		}
		for _, name := range []string{"cashierName", "documentAddress", "documentPlace", "reportAttribute", "reportDataHex"} {
			old := fields[name]
			fields[name] = old.(string) + "Я"
			if name == "reportDataHex" {
				fields[name] = old.(string) + "ab"
			}
			body, _ := json.Marshal(fields)
			if err := command.Validate(body); err == nil {
				t.Errorf("0x%02X accepted overlong %s", code, name)
			}
			fields[name] = old
		}
	}
}

func TestFFD12ShiftDataPresencePairsAndCashierINN(t *testing.T) {
	for _, code := range []byte{0xE4, 0xE5} {
		command := ffd12ShiftFind(t, ffd12ShiftCommands(registrationCommands()), code)
		for _, invalid := range []string{
			`{}`,
			`{"cashierName":"A","documentAddress":"B"}`,
			`{"cashierName":"A","documentPlace":"C"}`,
			`{"documentAddress":"B","documentPlace":"C"}`,
			`{"cashierName":"A","documentAddress":"B","documentPlace":"C","cashierInn":"1234567890"}`,
			`{"cashierName":"A","documentAddress":"B","documentPlace":"C","cashierInn":"12345678901A"}`,
			`{"cashierName":"A","documentAddress":"B","documentPlace":"C","reportAttribute":"R"}`,
			`{"cashierName":"A","documentAddress":"B","documentPlace":"C","reportDataHex":"00"}`,
			`{"cashierName":"A","documentAddress":"B","documentPlace":"C","reportAttribute":null,"reportDataHex":""}`,
			`{"cashierName":"A","documentAddress":"B","documentPlace":"C","reportAttribute":"R","reportDataHex":"not-hex"}`,
			`{"cashierName":"😀","documentAddress":"B","documentPlace":"C"}`,
			`{"cashierName":"A","documentAddress":"B","documentPlace":"C","unexpected":1}`,
		} {
			if err := command.Validate(json.RawMessage(invalid)); err == nil {
				t.Errorf("0x%02X accepted invalid shift data %s", code, invalid)
			}
		}
		// The supplement specifies maximum lengths, not positive minimums. The
		// driver preserves that distinction instead of inventing stricter bounds.
		if err := command.Validate(json.RawMessage(`{"cashierName":"","documentAddress":"","documentPlace":"","reportAttribute":"","reportDataHex":""}`)); err != nil {
			t.Fatalf("0x%02X imposed an undocumented minimum: %v", code, err)
		}
	}
}

func TestFFD12BaseCashierCommandKeepsItsExistingWireContract(t *testing.T) {
	command := ffd12ShiftFind(t, ffd12ShiftCommands(registrationCommands()), 0x2F)
	_, sent, err := ffd12ShiftExecute(t, command, `{"cashierName":"A","cashierInn":"123456789012"}`, nil)
	if err != nil || hex.EncodeToString(sent) != "fd03010041b3040c00313233343536373839303132" {
		t.Fatalf("cashier compatibility sent % X, error %v", sent, err)
	}
	for _, invalid := range []string{`{}`, `{"cashierName":"A","cashierInn":"1234567890"}`, `{"cashierName":"A","cashierInn":"12345678901A"}`} {
		if err := command.Validate(json.RawMessage(invalid)); err == nil {
			t.Errorf("cashier compatibility accepted %s", invalid)
		}
	}
}

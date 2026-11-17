package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/dronm/goterminalfa/internal/protocol"
)

func ffd12ClosureTestCommand(t *testing.T, name string) Command {
	t.Helper()
	for _, command := range ffd12FiscalStorageClosureCommands() {
		if command.Definition.Name == name {
			return command
		}
	}
	t.Fatalf("missing fiscal-storage closure command %s", name)
	return Command{}
}

func ffd12ClosureTestExecute(t *testing.T, name, params string, response []byte) (any, []byte, error) {
	t.Helper()
	command := ffd12ClosureTestCommand(t, name)
	if err := command.Validate(json.RawMessage(params)); err != nil {
		return nil, nil, err
	}
	var sent []byte
	device := testDevice("ffd-closure", func(_ context.Context, code byte, payload []byte) ([]byte, error) {
		want := map[string]byte{"beginFiscalStorageClosure": 0x14, "sendFiscalStorageClosureData": 0x17, "finalizeFiscalStorageClosure": 0x28}[name]
		if code != want {
			t.Fatalf("sent 0x%02X, want 0x%02X", code, want)
		}
		sent = append([]byte{}, payload...)
		return response, nil
	}, 1)
	result, err := command.Execute(context.Background(), device, json.RawMessage(params))
	return result, sent, err
}

func TestFFD12ClosureCataloguePolicyAndContextMetadata(t *testing.T) {
	if len(ffd12FiscalStorageClosureCommands()) != 3 {
		t.Fatal("expected three closure command definitions")
	}
	for _, command := range ffd12FiscalStorageClosureCommands() {
		definition := command.Definition
		if !definition.Implemented || definition.ReadOnly || !definition.IdempotencyRequired || !definition.OperationRequired || definition.ProtocolProfile != "ffd1.2" {
			t.Fatalf("wrong closure workflow policy: %+v", definition)
		}
		if definition.Source != ffd12ExtraSource(13) || definition.TitleRU == "" || definition.DescriptionRU == "" {
			t.Fatalf("missing closure supplement metadata: %+v", definition)
		}
	}
	finalizer := ffd12ClosureTestCommand(t, "finalizeFiscalStorageClosure").Definition
	if finalizer.ProtocolCode != "0x28" || finalizer.Aliases == nil || len(finalizer.Aliases) != 0 || !reflect.DeepEqual(finalizer.Output, registrationFiscalResult()) {
		t.Fatalf("closure 0x28 must retain its own response schema and no numeric aliases: %+v", finalizer)
	}
	data := ffd12ClosureTestCommand(t, "sendFiscalStorageClosureData").Definition
	if data.RequestLayout != "tlv" || data.ResponseLayout != "positional" || len(data.Output) != 0 {
		t.Fatalf("wrong closure data layout: %+v", data)
	}
}

func TestFFD12ClosureNoInputAndEmptyStartResponse(t *testing.T) {
	for _, name := range []string{"beginFiscalStorageClosure", "finalizeFiscalStorageClosure"} {
		command := ffd12ClosureTestCommand(t, name)
		if err := command.Validate(json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		for _, params := range []string{`{"suppressPrint":1}`, `{"unexpected":1}`, `null`, `[]`} {
			if err := command.Validate(json.RawMessage(params)); err == nil {
				t.Fatalf("%s accepted undocumented parameters %s", name, params)
			}
		}
	}
	result, sent, err := ffd12ClosureTestExecute(t, "beginFiscalStorageClosure", `{}`, nil)
	if err != nil || len(sent) != 0 || len(settingsObject(t, result)) != 0 {
		t.Fatalf("closure start result %#v, sent % X, error %v", result, sent, err)
	}
	if _, _, err := ffd12ClosureTestExecute(t, "beginFiscalStorageClosure", `{}`, []byte{0}); !errors.Is(err, protocol.ErrMalformedFrame) {
		t.Fatalf("closure start accepted unexpected response data: %v", err)
	}
}

func TestFFD12ClosureDataGoldenTLVsAndOptionalPair(t *testing.T) {
	for _, test := range []struct {
		params string
		wire   string
	}{
		{`{"documentAddress":"A","documentPlace":"P"}`, "f103010041a304010050"},
		{`{"documentAddress":"Тест","documentPlace":"P","reportAttribute":"R","reportDataHex":"00fF"}`, "f103040092a5e1e2a30401005002050100520305020000ff"},
		{`{"documentAddress":"","documentPlace":"","reportAttribute":"","reportDataHex":""}`, "f1030000a30400000205000003050000"},
	} {
		result, sent, err := ffd12ClosureTestExecute(t, "sendFiscalStorageClosureData", test.params, nil)
		if err != nil || !bytes.Equal(sent, settingsHex(t, test.wire)) || len(settingsObject(t, result)) != 0 {
			t.Fatalf("closure data sent % X, want %s, result %#v, error %v", sent, test.wire, result, err)
		}
	}
	for _, params := range []string{
		`{}`, `{"documentAddress":"A"}`, `{"documentPlace":"P"}`,
		`{"documentAddress":"A","documentPlace":"P","reportAttribute":"R"}`,
		`{"documentAddress":"A","documentPlace":"P","reportDataHex":"00"}`,
		`{"documentAddress":"A","documentPlace":"P","reportAttribute":"R","reportDataHex":"0"}`,
		`{"documentAddress":"A","documentPlace":"P","reportAttribute":"R","reportDataHex":"zz"}`,
		`{"documentAddress":"A","documentPlace":"P","reportAttribute":"😀","reportDataHex":"00"}`,
		`{"documentAddress":null,"documentPlace":"P"}`,
		`{"documentAddress":"A","documentPlace":1}`,
		`{"documentAddress":"A","documentPlace":"P","unknown":1}`,
	} {
		if err := ffd12ClosureTestCommand(t, "sendFiscalStorageClosureData").Validate(json.RawMessage(params)); err == nil {
			t.Errorf("accepted invalid closure data %s", params)
		}
	}
	if _, _, err := ffd12ClosureTestExecute(t, "sendFiscalStorageClosureData", `{"documentAddress":"A","documentPlace":"P"}`, []byte{0}); !errors.Is(err, protocol.ErrMalformedFrame) {
		t.Fatalf("closure data accepted unexpected response bytes: %v", err)
	}
}

func TestFFD12ClosureDataEncodedBounds(t *testing.T) {
	params := map[string]any{
		"documentAddress": strings.Repeat("Я", 256),
		"documentPlace":   strings.Repeat("Я", 256),
		"reportAttribute": strings.Repeat("Я", 32),
		"reportDataHex":   strings.Repeat("ab", 32),
	}
	command := ffd12ClosureTestCommand(t, "sendFiscalStorageClosureData")
	raw, _ := json.Marshal(params)
	if err := command.Validate(raw); err != nil {
		t.Fatalf("rejected exact CP866/binary bounds: %v", err)
	}
	for _, name := range []string{"documentAddress", "documentPlace", "reportAttribute", "reportDataHex"} {
		old := params[name].(string)
		params[name] = old + "Я"
		if name == "reportDataHex" {
			params[name] = old + "ab"
		}
		raw, _ = json.Marshal(params)
		if err := command.Validate(raw); err == nil {
			t.Fatalf("accepted closure field %s beyond encoded bound", name)
		}
		params[name] = old
	}
}

func TestFFD12ClosureFinalizerStrictEightByteFiscalResult(t *testing.T) {
	response := []byte{0xEF, 0xCD, 0xAB, 0x89, 0x98, 0xBA, 0xDC, 0xFE}
	result, sent, err := ffd12ClosureTestExecute(t, "finalizeFiscalStorageClosure", `{}`, response)
	want := map[string]any{"documentNumber": uint64(0x89ABCDEF), "fiscalSign": uint64(0xFEDCBA98)}
	if err != nil || len(sent) != 0 || !reflect.DeepEqual(result, want) {
		t.Fatalf("closure finalization result %#v, sent % X, error %v", result, sent, err)
	}
	for length := 0; length <= 17; length++ {
		if length == 8 {
			continue
		}
		if _, _, err := ffd12ClosureTestExecute(t, "finalizeFiscalStorageClosure", `{}`, make([]byte, length)); !errors.Is(err, protocol.ErrMalformedFrame) {
			t.Errorf("closure finalizer accepted response length %d: %v", length, err)
		}
	}
}

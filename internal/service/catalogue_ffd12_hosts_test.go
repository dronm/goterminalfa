package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dronm/goterminalfa/internal/protocol"
)

func ffd12HostTestCommand(t *testing.T, code byte) Command {
	t.Helper()
	for _, command := range ffd12HostCommands() {
		if command.Definition.ProtocolCode == fmt.Sprintf("0x%02X", code) {
			return command
		}
	}
	t.Fatalf("host command 0x%02X not found", code)
	return Command{}
}

func ffd12HostTestExecute(t *testing.T, code byte, params string, response []byte) (any, []byte, error) {
	t.Helper()
	command := ffd12HostTestCommand(t, code)
	if err := command.Validate(json.RawMessage(params)); err != nil {
		return nil, nil, err
	}
	var payload []byte
	device := testDevice("ffd-host", func(_ context.Context, actual byte, data []byte) ([]byte, error) {
		if actual != code {
			t.Fatalf("sent 0x%02X, want 0x%02X", actual, code)
		}
		payload = append([]byte{}, data...)
		return response, nil
	}, 1)
	result, err := command.Execute(context.Background(), device, json.RawMessage(params))
	return result, payload, err
}

func TestFFD12HostCataloguePolicyAndSource(t *testing.T) {
	if len(ffd12HostCommands()) != 4 {
		t.Fatal("expected four host settings commands")
	}
	for _, code := range []byte{0x82, 0x83, 0x84, 0x85} {
		definition := ffd12HostTestCommand(t, code).Definition
		readOnly := code&1 != 0
		if !definition.Implemented || definition.ReadOnly != readOnly || definition.IdempotencyRequired == readOnly || definition.OperationRequired {
			t.Fatalf("wrong standalone read/write policy for 0x%02X: %+v", code, definition)
		}
		if definition.Source != ffd12ExtraSource(4) || definition.TitleRU == "" || definition.DescriptionRU == "" {
			t.Fatalf("missing supplement provenance for 0x%02X", code)
		}
		if readOnly && (definition.RequestLayout != "positional" || definition.ResponseLayout != "tlv" || len(definition.Input) != 0) {
			t.Fatalf("incorrect getter layouts for 0x%02X", code)
		}
		if !readOnly && (definition.RequestLayout != "tlv" || definition.ResponseLayout != "positional" || len(definition.Output) != 0) {
			t.Fatalf("incorrect setter layouts for 0x%02X", code)
		}
	}
}

func TestFFD12HostSettingsGoldenTLVsAndCP866RoundTrip(t *testing.T) {
	for _, tc := range []struct {
		setter byte
		getter byte
		params string
		wire   string
	}{
		{0x82, 0x83, `{"host":"Тест","tcpPort":4660,"timeoutSeconds":30}`, "6275040092a5e1e2637502003412647502001e00"},
		{0x84, 0x85, `{"host":"Тест","tcpPort":4660}`, "6c75040092a5e1e26d7502003412"},
	} {
		wire := settingsHex(t, tc.wire)
		result, sent, err := ffd12HostTestExecute(t, tc.setter, tc.params, nil)
		if err != nil || !bytes.Equal(sent, wire) || len(settingsObject(t, result)) != 0 {
			t.Fatalf("0x%02X sent % X, want % X, result %#v, error %v", tc.setter, sent, wire, result, err)
		}
		result, sent, err = ffd12HostTestExecute(t, tc.getter, `{}`, wire)
		if err != nil || len(sent) != 0 {
			t.Fatalf("0x%02X getter sent % X, error %v", tc.getter, sent, err)
		}
		object := settingsObject(t, result)
		if object["host"] != "Тест" || object["tcpPort"] != float64(4660) {
			t.Fatalf("0x%02X wrong settings %#v", tc.getter, object)
		}
		if tc.getter == 0x83 && object["timeoutSeconds"] != float64(30) {
			t.Fatalf("OISM timeout %#v", object)
		}
		if tc.getter == 0x85 && len(object) != 2 {
			t.Fatalf("key-update getter fabricated extra settings %#v", object)
		}
	}
}

func TestFFD12HostSettingsExplicitFieldsAndBounds(t *testing.T) {
	for _, code := range []byte{0x82, 0x84} {
		command := ffd12HostTestCommand(t, code)
		for _, value := range []int{0, 65535} {
			params := map[string]any{"host": "", "tcpPort": value}
			if code == 0x82 {
				params["timeoutSeconds"] = value
			}
			raw, _ := json.Marshal(params)
			if err := command.Validate(raw); err != nil {
				t.Fatalf("0x%02X rejected empty host/UINT16 boundary %d: %v", code, value, err)
			}
		}
		params := map[string]any{"host": strings.Repeat("Я", 128), "tcpPort": 1}
		if code == 0x82 {
			params["timeoutSeconds"] = 1
		}
		raw, _ := json.Marshal(params)
		_, payload, err := ffd12HostTestExecute(t, code, string(raw), nil)
		if err != nil || len(payload) < 132 || payload[2] != 128 || payload[3] != 0 {
			t.Fatalf("0x%02X rejected 128 encoded bytes: % X, %v", code, payload, err)
		}
		params["host"] = strings.Repeat("Я", 129)
		raw, _ = json.Marshal(params)
		if err := command.Validate(raw); err == nil {
			t.Fatalf("0x%02X accepted 129 encoded host bytes", code)
		}
		params["host"] = "host text / without DNS validation"
		raw, _ = json.Marshal(params)
		if err := command.Validate(raw); err != nil {
			t.Fatalf("0x%02X invented a host-format restriction: %v", code, err)
		}
		for _, name := range []string{"host", "tcpPort", "timeoutSeconds"} {
			value, present := params[name]
			if !present {
				continue
			}
			delete(params, name)
			raw, _ = json.Marshal(params)
			if err := command.Validate(raw); err == nil {
				t.Fatalf("0x%02X inferred missing %s", code, name)
			}
			params[name] = value
		}
	}
}

func TestFFD12HostSettingsRejectInvalidInputs(t *testing.T) {
	for _, code := range []byte{0x82, 0x84} {
		params := map[string]any{"host": "example", "tcpPort": 1}
		if code == 0x82 {
			params["timeoutSeconds"] = 30
		}
		for field, values := range map[string][]any{
			"host":           {nil, 1, true, "😀"},
			"tcpPort":        {nil, -1, 65536, 1.5, true, "1"},
			"timeoutSeconds": {nil, -1, 65536, 1.5, true, "30"},
		} {
			old, present := params[field]
			if !present {
				continue
			}
			for _, value := range values {
				params[field] = value
				raw, _ := json.Marshal(params)
				if err := ffd12HostTestCommand(t, code).Validate(raw); err == nil {
					t.Errorf("0x%02X accepted %s", code, raw)
				}
			}
			params[field] = old
		}
		params["unknown"] = 1
		raw, _ := json.Marshal(params)
		if err := ffd12HostTestCommand(t, code).Validate(raw); err == nil {
			t.Fatalf("0x%02X accepted unknown input", code)
		}
		if _, _, err := ffd12HostTestExecute(t, code, string(raw[:len(raw)-1]), nil); err == nil {
			t.Fatalf("0x%02X accepted malformed JSON", code)
		}
	}
	for _, code := range []byte{0x83, 0x85} {
		if err := ffd12HostTestCommand(t, code).Validate(json.RawMessage(`{"host":"unexpected"}`)); err == nil {
			t.Fatalf("getter 0x%02X accepted parameters", code)
		}
	}
}

func TestFFD12HostGettersPreserveEmptyHostsAndRepeatedUnknownTags(t *testing.T) {
	for _, tc := range []struct {
		code byte
		wire string
	}{
		{0x83, "62750000637502000000647502000000"},
		{0x85, "6c7500006d7502000000"},
	} {
		wire := append(settingsHex(t, tc.wire), settingsHex(t, "feff0100aafeff0100bb")...)
		result, _, err := ffd12HostTestExecute(t, tc.code, `{}`, wire)
		if err != nil {
			t.Fatal(err)
		}
		object := settingsObject(t, result)
		if object["host"] != "" || object["tcpPort"] != float64(0) {
			t.Fatalf("0x%02X changed unconfigured fields: %#v", tc.code, object)
		}
		unknown := object["unknownTags"].([]any)
		if len(unknown) != 2 || unknown[0].(map[string]any)["tag"] != float64(65534) || unknown[0].(map[string]any)["valueHex"] != "aa" || unknown[1].(map[string]any)["valueHex"] != "bb" {
			t.Fatalf("0x%02X lost repeated unknown tag order: %#v", tc.code, object)
		}
	}
}

func TestFFD12HostResponsesRejectMalformedTLVsAndUnexpectedSetterData(t *testing.T) {
	for _, tc := range []struct {
		code byte
		wire string
	}{
		{0x83, ""},
		{0x83, "62750000637502000100"},
		{0x83, "627500006375010001647502001e00"},
		{0x83, "62750000637502000100647503001e0000"},
		{0x83, "6275000062750000637502000100647502001e00"},
		{0x83, "6275020041"},
		{0x85, "6c750000"},
		{0x85, "6c7500006d750300010000"},
		{0x85, "6c7500006d75020001006d7502000200"},
		{0x85, "6c7500006d7502000100ff"},
	} {
		if _, _, err := ffd12HostTestExecute(t, tc.code, `{}`, settingsHex(t, tc.wire)); !errors.Is(err, protocol.ErrMalformedFrame) {
			t.Errorf("0x%02X malformed response %s error %v", tc.code, tc.wire, err)
		}
	}
	for _, tc := range []struct {
		code   byte
		params string
	}{
		{0x82, `{"host":"example","tcpPort":1,"timeoutSeconds":30}`},
		{0x84, `{"host":"example","tcpPort":1}`},
	} {
		if _, _, err := ffd12HostTestExecute(t, tc.code, tc.params, []byte{0}); !errors.Is(err, protocol.ErrMalformedFrame) {
			t.Errorf("0x%02X accepted unexpected setter response byte: %v", tc.code, err)
		}
	}
	for _, code := range []byte{0x83, 0x85} {
		hostTag, portTag := uint16(30050), uint16(30051)
		if code == 0x85 {
			hostTag, portTag = 30060, 30061
		}
		wire, _ := AppendTLV(nil, hostTag, bytes.Repeat([]byte{'a'}, 129))
		wire, _ = AppendTLV(wire, portTag, []byte{1, 0})
		if code == 0x83 {
			wire, _ = AppendTLV(wire, 30052, []byte{30, 0})
		}
		if _, _, err := ffd12HostTestExecute(t, code, `{}`, wire); !errors.Is(err, protocol.ErrMalformedFrame) {
			t.Errorf("0x%02X accepted overlong host response: %v", code, err)
		}
	}
}

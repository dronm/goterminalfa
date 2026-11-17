package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/dronm/goterminalfa/internal/protocol"
)

func ffd12DiagnosticCommand(t *testing.T, name string) Command {
	t.Helper()
	for _, command := range ffd12DiagnosticCommands() {
		if command.Definition.Name == name {
			return command
		}
	}
	t.Fatalf("missing FFD 1.2 diagnostic command %q", name)
	return Command{}
}

func ffd12DiagnosticResponse(t *testing.T, name string, code byte, response []byte) (map[string]any, error) {
	t.Helper()
	device := testDevice("ffd12-diagnostics", func(_ context.Context, got byte, payload []byte) ([]byte, error) {
		if got != code || len(payload) != 0 {
			t.Errorf("diagnostic request = %02X % X; want %02X without parameters", got, payload, code)
		}
		return response, nil
	}, 1)
	device.ProtocolProfile = "ffd1.2"
	value, err := ffd12DiagnosticCommand(t, name).Execute(context.Background(), device, json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("diagnostic result is not an object: %T", value)
	}
	return object, nil
}

func TestFFD12DiagnosticsDecodeGoldenResponses(t *testing.T) {
	for _, test := range []struct {
		name string
		code byte
		data []byte
		want map[string]any
	}{
		{"getFiscalStorageFormat", 0x0F, []byte{4, 4}, map[string]any{"ffdVersion": uint64(4), "supportedFfdVersion": uint64(4)}},
		{"getOismNotificationStatus", 0x9E, []byte{2, 0xDC, 0xFE, 0xEF, 0xCD, 0xAB, 0x89, 24, 2, 29, 23, 59, 75}, map[string]any{"state": uint64(2), "queuedNotifications": uint64(0xFEDC), "currentNotificationNumber": uint64(0x89ABCDEF), "currentNotificationDateTime": "2024-02-29T23:59", "storageUsedPercent": uint64(75)}},
		{"getFiscalStorageMemory", 0x9D, []byte{0x98, 0xBA, 0xDC, 0xFE, 0x67, 0x45, 0x23, 0x01, 95}, map[string]any{"remainingLongTermDocuments": uint64(0xFEDCBA98), "freeShortTermStorageKB": uint64(0x01234567), "notificationStorageUsedPercent": uint64(95)}},
		{"getServiceParameters", 0xA2, []byte{' ', 0x92, 0xA5, 0xE0, 0xAC, 0xA8, 0xAD, 0xA0, 0xAB, '-', 0x94, 0x80, ';', '=', '\r', '\n', ' '}, map[string]any{"parameters": " Терминал-ФА;=\r\n "}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := ffd12DiagnosticResponse(t, test.name, test.code, test.data)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("decoded diagnostic = %#v; want %#v", got, test.want)
			}
		})
	}
}

func TestFFD12DiagnosticsOptionalMemoryByteAndEmptyServiceText(t *testing.T) {
	for _, response := range [][]byte{
		{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
		{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0},
	} {
		got, err := ffd12DiagnosticResponse(t, "getFiscalStorageMemory", 0x9D, response)
		if err != nil {
			t.Fatal(err)
		}
		if got["remainingLongTermDocuments"] != uint64(0xFFFFFFFF) || got["freeShortTermStorageKB"] != uint64(0xFFFFFFFF) {
			t.Fatalf("UINT32 boundary values were lost: %#v", got)
		}
		value, present := got["notificationStorageUsedPercent"]
		if len(response) == 8 && present || len(response) == 9 && (!present || value != uint64(0)) {
			t.Fatalf("absent and zero notification usage must remain distinct: response=% X result=%#v", response, got)
		}
	}
	for _, response := range [][]byte{nil, {}} {
		got, err := ffd12DiagnosticResponse(t, "getServiceParameters", 0xA2, response)
		if err != nil || !reflect.DeepEqual(got, map[string]any{"parameters": ""}) {
			t.Fatalf("empty service string was not preserved: result=%#v error=%v", got, err)
		}
	}
}

func TestFFD12DiagnosticsZeroDateAndFutureCodesArePreserved(t *testing.T) {
	for _, active := range []byte{0, 1, 3, 4, 255} {
		got, err := ffd12DiagnosticResponse(t, "getFiscalStorageFormat", 0x0F, []byte{active, 255})
		if err != nil || got["ffdVersion"] != uint64(active) || got["supportedFfdVersion"] != uint64(255) {
			t.Fatalf("documented or future FFD code was rejected: result=%#v error=%v", got, err)
		}
	}
	response := []byte{255, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0, 0, 0, 0, 0, 255}
	got, err := ffd12DiagnosticResponse(t, "getOismNotificationStatus", 0x9E, response)
	if err != nil {
		t.Fatal(err)
	}
	if got["state"] != uint64(255) || got["queuedNotifications"] != uint64(0xFFFF) || got["currentNotificationNumber"] != uint64(0xFFFFFFFF) || got["storageUsedPercent"] != uint64(255) {
		t.Fatalf("unknown state or unsigned boundary was lost: %#v", got)
	}
	date, present := got["currentNotificationDateTime"]
	if !present || date != nil {
		t.Fatalf("all-zero date must be an explicit null: %#v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil || string(fields["currentNotificationDateTime"]) != "null" {
		t.Fatalf("zero date does not serialize as null: %s error=%v", encoded, err)
	}
}

func TestFFD12DiagnosticsRejectMalformedResponses(t *testing.T) {
	for _, test := range []struct {
		name string
		code byte
		min  int
		max  int
	}{
		{"getFiscalStorageFormat", 0x0F, 2, 2},
		{"getOismNotificationStatus", 0x9E, 13, 13},
		{"getFiscalStorageMemory", 0x9D, 8, 9},
	} {
		for length := 0; length <= test.max+2; length++ {
			if length >= test.min && length <= test.max {
				continue
			}
			if _, err := ffd12DiagnosticResponse(t, test.name, test.code, make([]byte, length)); !errors.Is(err, protocol.ErrMalformedFrame) {
				t.Fatalf("%s accepted response length %d or lost malformed-frame classification: %v", test.name, length, err)
			}
		}
	}
	for _, date := range [][]byte{
		{26, 0, 1, 0, 0}, {26, 13, 1, 0, 0}, {26, 1, 0, 0, 0},
		{23, 2, 29, 0, 0}, {26, 4, 31, 0, 0}, {26, 1, 1, 24, 0}, {26, 1, 1, 0, 60},
	} {
		response := append([]byte{0, 0, 0, 0, 0, 0, 0}, date...)
		response = append(response, 0)
		if _, err := ffd12DiagnosticResponse(t, "getOismNotificationStatus", 0x9E, response); !errors.Is(err, protocol.ErrMalformedFrame) {
			t.Fatalf("accepted invalid notification date % X or lost malformed-frame classification: %v", date, err)
		}
	}
}

func TestFFD12DiagnosticsMetadataAndEmptyParameters(t *testing.T) {
	commands := ffd12DiagnosticCommands()
	if len(commands) != 4 {
		t.Fatalf("got %d diagnostic commands; want four", len(commands))
	}
	for _, command := range commands {
		definition := command.Definition
		if !definition.Implemented || !definition.ReadOnly || definition.IdempotencyRequired || definition.OperationRequired || len(definition.Input) != 0 {
			t.Fatalf("diagnostic must be a standalone read with no input: %+v", definition)
		}
		if definition.Source.Version != "FFD 1.2" || definition.Source.Page != 5 && definition.Source.Page != 6 && definition.Source.Page != 28 || definition.TitleRU == "" || definition.DescriptionRU == "" {
			t.Fatalf("diagnostic provenance or documentation is missing: %+v", definition)
		}
		if err := command.Validate(json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		for _, params := range []string{`{"unexpected":1}`, `null`, `[]`} {
			if err := command.Validate(json.RawMessage(params)); err == nil {
				t.Fatalf("%s accepted invalid parameters %s", definition.Name, params)
			}
		}
	}
	format := ffd12DiagnosticCommand(t, "getFiscalStorageFormat").Definition.Output
	if len(format[0].Enum) != 4 || format[0].Enum["0"] == "" || format[0].Enum["1"] == "" || format[0].Enum["3"] == "" || format[0].Enum["4"] == "" || len(format[1].Enum) != 0 {
		t.Fatalf("format dictionary does not preserve the documented and future codes: %+v", format)
	}
	memory := ffd12DiagnosticCommand(t, "getFiscalStorageMemory").Definition.Output
	if len(memory) != 3 || memory[2].Required || memory[2].WireType != "BYTE" {
		t.Fatalf("notification memory byte must be optional: %+v", memory)
	}
}

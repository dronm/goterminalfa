package service

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	terminalfa "github.com/dronm/goterminalfa"
)

type informationExchange func(context.Context, byte, []byte) ([]byte, error)

func (f informationExchange) Exchange(ctx context.Context, code byte, data []byte) ([]byte, error) {
	return f(ctx, code, data)
}

func findInformationCommand(t *testing.T, name string) Command {
	t.Helper()
	for _, cmd := range informationCommands() {
		if cmd.Definition.Name == name {
			return cmd
		}
	}
	t.Fatalf("missing information command %q", name)
	return Command{}
}

func informationResponse(t *testing.T, name string, code byte, payload []byte) (any, error) {
	t.Helper()
	cmd := findInformationCommand(t, name)
	device := NewDevice("information-test", "serial", "", terminalfa.NewClient(informationExchange(func(_ context.Context, got byte, request []byte) ([]byte, error) {
		if got != code || len(request) != 0 {
			t.Errorf("wire command = %02X % X, want %02X without payload", got, request, code)
		}
		return payload, nil
	})), 1)
	return cmd.Execute(context.Background(), device, json.RawMessage(`{}`))
}

func informationObject(t *testing.T, value any) map[string]any {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestInformationCommandsDecodeDocumentedLayouts(t *testing.T) {
	status := []byte{3, 4, 1, 1, 8, 26, 10, 30, 11, 19}
	status = append(status, []byte("1234567890123456")...)
	status = binary.LittleEndian.AppendUint32(status, 111)
	registration := []byte("123456" + strings.Repeat(" ", 14) + "1234567890  ")
	registration = append(registration, 4, 2, 1)
	network, _ := hex.DecodeString("3a0aa8c000ffffff010aa8c0")
	cases := []struct {
		name string
		code byte
		data []byte
		want map[string]any
	}{
		{"getSerialNumber", 0x02, []byte("550101046182"), map[string]any{"serialNumber": "550101046182"}},
		{"getFirmwareVersion", 0x03, []byte("1.0.21"), map[string]any{"firmwareVersion": "1.0.21"}},
		{"getModel", 0x04, []byte{0x92, 0xa5, 0xe0, 0xac, 0xa8, 0xad, 0xa0, 0xab, 0x2d, 0x94, 0x80}, map[string]any{"model": "Терминал-ФА"}},
		{"getFiscalStorageSerialNumber", 0x05, []byte("1234567890123456"), map[string]any{"fiscalStorageSerialNumber": "1234567890123456"}},
		{"getFiscalStorageFirmwareVersion", 0x06, []byte("fn-1.2"), map[string]any{"firmwareVersion": "fn-1.2"}},
		{"getFiscalStorageValidity", 0x07, []byte{26, 10, 31, 3, 2}, map[string]any{"expiresOn": "2026-10-31", "availableReregistrations": float64(3), "completedReregistrations": float64(2)}},
		{"getFiscalStorageStatus", 0x08, status, map[string]any{"lifecycleFlags": float64(3), "currentDocument": float64(4), "documentDataReceived": float64(1), "shiftOpen": float64(1), "warningFlags": float64(8), "lastDocumentDateTime": "2026-10-30T11:19", "fiscalStorageSerialNumber": "1234567890123456", "lastDocumentNumber": float64(111)}},
		{"getFiscalStorageErrors", 0x09, []byte{0x00, 0xff, 0x17}, map[string]any{"dataHex": "00ff17"}},
		{"getFiscalStorageErrors", 0x09, nil, map[string]any{"dataHex": ""}},
		{"getRegistrationParameters", 0x0A, registration, map[string]any{"registrationNumber": "123456", "taxpayerINN": "1234567890", "operatingModes": float64(4), "taxationSystems": float64(2), "paymentAgentFlags": float64(1)}},
		{"getConfigurationVersion", 0x0B, []byte("1.0.16"), map[string]any{"configurationVersion": "1.0.16"}},
		{"getNetworkParameters", 0x0E, network, map[string]any{"ipAddress": "192.168.10.58", "subnetMask": "255.255.255.0", "defaultGateway": "192.168.10.1"}},
		{"getOFDExchangeStatus", 0x50, []byte{1, 2, 3, 0, 111, 0, 0, 0, 26, 10, 30, 11, 19}, map[string]any{"exchangeStatus": float64(1), "messageReadState": float64(2), "pendingMessages": float64(3), "firstDocumentNumber": float64(111), "firstDocumentDateTime": "2026-10-30T11:19"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, err := informationResponse(t, tc.name, tc.code, tc.data)
			if err != nil {
				t.Fatal(err)
			}
			if got := informationObject(t, value); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("decoded response = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestInformationCommandsValidateNoParametersAndDescribePolicy(t *testing.T) {
	commands := informationCommands()
	if len(commands) != 12 {
		t.Fatalf("got %d information commands, want 12", len(commands))
	}
	for _, cmd := range commands {
		t.Run(cmd.Definition.Name, func(t *testing.T) {
			if !cmd.Definition.ReadOnly || cmd.Definition.IdempotencyRequired {
				t.Fatal("diagnostic commands must remain fresh repeatable reads")
			}
			if cmd.Definition.TitleRU == "" || cmd.Definition.DescriptionRU == "" || cmd.Definition.Source.Page < 10 || cmd.Definition.Source.Page > 12 {
				t.Fatalf("incomplete documentation: %+v", cmd.Definition)
			}
			if err := cmd.Validate(json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			for _, invalid := range []string{`{"unexpected":1}`, `null`, `[]`} {
				if err := cmd.Validate(json.RawMessage(invalid)); err == nil {
					t.Fatalf("accepted unexpected parameters %s", invalid)
				}
			}
		})
	}
	serial := findInformationCommand(t, "getSerialNumber")
	foundID := false
	for _, alias := range serial.Definition.Aliases {
		foundID = foundID || alias == "ID"
	}
	if !foundID {
		t.Fatal("lost backward-compatible ID alias")
	}
}

func TestInformationCommandsRejectMalformedFixedResponses(t *testing.T) {
	cases := []struct {
		name string
		code byte
		data []byte
	}{
		{"getSerialNumber", 0x02, []byte("55010104618X")},
		{"getFiscalStorageValidity", 0x07, []byte{26, 10, 31, 3}},
		{"getFiscalStorageValidity", 0x07, []byte{26, 2, 30, 3, 2}},
		{"getFiscalStorageStatus", 0x08, make([]byte, 29)},
		{"getRegistrationParameters", 0x0A, make([]byte, 34)},
		{"getNetworkParameters", 0x0E, make([]byte, 11)},
		{"getNetworkParameters", 0x0E, make([]byte, 13)},
		{"getOFDExchangeStatus", 0x50, make([]byte, 12)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := informationResponse(t, tc.name, tc.code, tc.data); err == nil {
				t.Fatalf("accepted malformed response: % X", tc.data)
			}
		})
	}
}

func TestInformationStatusZeroDateAndUnknownFlagsArePreserved(t *testing.T) {
	data := []byte{15, 250, 0, 0, 128, 0, 0, 0, 0, 0}
	data = append(data, []byte("1234567890123456")...)
	data = append(data, 0, 0, 0, 0)
	value, err := informationResponse(t, "getFiscalStorageStatus", 0x08, data)
	if err != nil {
		t.Fatal(err)
	}
	result := informationObject(t, value)
	if result["lastDocumentDateTime"] != nil || result["currentDocument"] != float64(250) || result["warningFlags"] != float64(128) {
		t.Fatalf("lost zero-date or firmware-extended status values: %+v", result)
	}
}

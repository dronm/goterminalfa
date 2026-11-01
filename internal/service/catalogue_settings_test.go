package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	terminalfa "github.com/dronm/goterminalfa"
)

type settingsExchangeFunc func(context.Context, byte, []byte) ([]byte, error)

func (f settingsExchangeFunc) Exchange(ctx context.Context, command byte, payload []byte) ([]byte, error) {
	return f(ctx, command, payload)
}

func settingsCommand(t *testing.T, code byte) Command {
	t.Helper()
	for _, command := range settingsCommands() {
		if command.Definition.ProtocolCode == fmt.Sprintf("0x%02X", code) {
			return command
		}
	}
	t.Fatalf("settings command 0x%02X is missing", code)
	return Command{}
}

func settingsExecute(t *testing.T, code byte, params string, response []byte) (any, []byte, error) {
	t.Helper()
	command := settingsCommand(t, code)
	if err := command.Validate(json.RawMessage(params)); err != nil {
		return nil, nil, err
	}
	var sent []byte
	device := &Device{Client: terminalfa.NewClient(settingsExchangeFunc(func(_ context.Context, actual byte, payload []byte) ([]byte, error) {
		if actual != code {
			t.Fatalf("sent command 0x%02X, wanted 0x%02X", actual, code)
		}
		sent = append([]byte{}, payload...)
		return response, nil
	}))}
	result, err := command.Execute(context.Background(), device, json.RawMessage(params))
	return result, sent, err
}

func settingsHex(t *testing.T, value string) []byte {
	t.Helper()
	data, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func settingsObject(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func TestSettingsCataloguePolicyAndMetadata(t *testing.T) {
	if len(settingsCommands()) != 8 {
		t.Fatalf("got %d settings commands", len(settingsCommands()))
	}
	for _, code := range []byte{0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x80, 0x81} {
		cmd := settingsCommand(t, code)
		readOnly := code&1 == 1
		if cmd.Definition.ReadOnly != readOnly || cmd.Definition.IdempotencyRequired == readOnly {
			t.Errorf("0x%02X incorrect persistence policy", code)
		}
		if !cmd.Definition.Implemented || cmd.Definition.TitleRU == "" || cmd.Definition.DescriptionRU == "" || cmd.Definition.Source.Page < 29 || cmd.Definition.Source.Page > 31 {
			t.Errorf("0x%02X missing implementation/source metadata", code)
		}
		if readOnly && cmd.Definition.ResponseLayout != "tlv" || !readOnly && cmd.Definition.RequestLayout != "tlv" {
			t.Errorf("0x%02X has incorrect TLV layout", code)
		}
	}
	if settingsCommand(t, 0x76).Definition.Input[1].Required || settingsCommand(t, 0x78).Definition.Input[0].Required {
		t.Fatal("documented optional address/model marked required")
	}
}

func TestNetworkSettingsGoldenTLVsAndRoundTrip(t *testing.T) {
	params := `{"dynamicAddress":0,"ipAddress":"192.168.1.10","subnetMask":"255.255.255.0","gateway":"192.168.1.1"}`
	wire := settingsHex(t, "317501000032750c003139322e3136382e312e313033750d003235352e3235352e3235352e3034750b003139322e3136382e312e31")
	_, sent, err := settingsExecute(t, 0x74, params, nil)
	if err != nil || !bytes.Equal(sent, wire) {
		t.Fatalf("network settings wire % X, error %v", sent, err)
	}
	withUnknown := append(append([]byte{}, wire...), 0xFE, 0xFF, 2, 0, 0xA0, 0xB1)
	result, sent, err := settingsExecute(t, 0x75, `{}`, withUnknown)
	if err != nil || len(sent) != 0 {
		t.Fatalf("network get wire % X, error %v", sent, err)
	}
	object := settingsObject(t, result)
	if object["dynamicAddress"] != float64(0) || object["ipAddress"] != "192.168.1.10" || object["subnetMask"] != "255.255.255.0" || object["gateway"] != "192.168.1.1" {
		t.Fatalf("incorrect network settings %#v", object)
	}
	unknown := object["unknownTags"].([]any)
	if len(unknown) != 1 || unknown[0].(map[string]any)["valueHex"] != "a0b1" {
		t.Fatalf("future TLV not preserved: %#v", object)
	}
	if err := settingsCommand(t, 0x74).Validate(json.RawMessage(`{"dynamicAddress":1,"ipAddress":"0.0.0.0","subnetMask":"0.0.0.0","gateway":"0.0.0.0"}`)); err != nil {
		t.Fatalf("DHCP placeholders rejected: %v", err)
	}
}

func TestSettingsRejectInvalidNetworkInputs(t *testing.T) {
	cases := []string{
		`{"dynamicAddress":1}`,
		`{"dynamicAddress":2,"ipAddress":"192.168.1.10","subnetMask":"255.255.255.0","gateway":"192.168.1.1"}`,
		`{"dynamicAddress":0,"ipAddress":"192.168.1.999","subnetMask":"255.255.255.0","gateway":"192.168.1.1"}`,
		`{"dynamicAddress":0,"ipAddress":"::1","subnetMask":"255.255.255.0","gateway":"192.168.1.1"}`,
		`{"dynamicAddress":0,"ipAddress":"192.168.1.10","subnetMask":"255.0.255.0","gateway":"192.168.1.1"}`,
		`{"dynamicAddress":0,"ipAddress":"192.168.1.10","subnetMask":"255.255.255.0","gateway":"bad"}`,
		`{"dynamicAddress":0,"ipAddress":"192.168.1.10","subnetMask":"255.255.255.0","gateway":"192.168.1.1","unknown":1}`,
		`{"dynamicAddress":0,"ipAddress":null,"subnetMask":"255.255.255.0","gateway":"192.168.1.1"}`,
	}
	for _, params := range cases {
		if err := settingsCommand(t, 0x74).Validate(json.RawMessage(params)); err == nil {
			t.Errorf("accepted invalid network params %s", params)
		}
	}
}

func TestOfdSettingsOptionalFallbackAndLittleEndian(t *testing.T) {
	params := `{"domain":"ofd.example","tcpPort":7777,"timerSeconds":30}`
	wire := settingsHex(t, "58750b006f66642e6578616d706c6536750200611e397502001e00")
	_, sent, err := settingsExecute(t, 0x76, params, nil)
	if err != nil || !bytes.Equal(sent, wire) {
		t.Fatalf("OFD settings wire % X, error %v", sent, err)
	}
	result, _, err := settingsExecute(t, 0x77, `{}`, wire)
	if err != nil {
		t.Fatal(err)
	}
	object := settingsObject(t, result)
	if object["domain"] != "ofd.example" || object["tcpPort"] != float64(7777) || object["timerSeconds"] != float64(30) {
		t.Fatalf("OFD fields %#v", object)
	}
	if _, ok := object["ipAddress"]; ok {
		t.Fatal("an omitted fallback address was fabricated")
	}
	fallbackParams := `{"domain":"ofd.example","ipAddress":"203.0.113.1","tcpPort":7777,"timerSeconds":30}`
	fallbackWire := settingsHex(t, "58750b006f66642e6578616d706c6535750b003230332e302e3131332e3136750200611e397502001e00")
	_, sent, err = settingsExecute(t, 0x76, fallbackParams, nil)
	if err != nil || !bytes.Equal(sent, fallbackWire) {
		t.Fatalf("fallback wire % X, error %v", sent, err)
	}
	if _, _, err := settingsExecute(t, 0x77, `{}`, append(append([]byte{}, wire...), 0x35, 0x75, 0, 0)); err != nil {
		t.Fatalf("unset fallback empty response TLV rejected: %v", err)
	}
}

func TestOfdSettingsValidationAndIntegerBoundaries(t *testing.T) {
	for _, params := range []string{
		`{"tcpPort":7777,"timerSeconds":30}`,
		`{"domain":"","tcpPort":7777,"timerSeconds":30}`,
		`{"domain":"   ","tcpPort":7777,"timerSeconds":30}`,
		`{"domain":"ofd.example","ipAddress":"","tcpPort":7777,"timerSeconds":30}`,
		`{"domain":"ofd.example","ipAddress":"2001:db8::1","tcpPort":7777,"timerSeconds":30}`,
		`{"domain":"ofd.example","tcpPort":65536,"timerSeconds":30}`,
		`{"domain":"ofd.example","tcpPort":7777,"timerSeconds":-1}`,
		`{"domain":"ofd.example","tcpPort":7777}`,
		`{"domain":"` + strings.Repeat("a", 129) + `","tcpPort":7777,"timerSeconds":30}`,
		`{"domain":"😀.example","tcpPort":7777,"timerSeconds":30}`,
	} {
		if err := settingsCommand(t, 0x76).Validate(json.RawMessage(params)); err == nil {
			t.Errorf("accepted invalid OFD params %s", params)
		}
	}
	for _, value := range []uint64{0, 65535} {
		params := fmt.Sprintf(`{"domain":"ofd.example","tcpPort":%d,"timerSeconds":%d}`, value, value)
		if err := settingsCommand(t, 0x76).Validate(json.RawMessage(params)); err != nil {
			t.Fatalf("documented UINT16 boundary %d rejected: %v", value, err)
		}
	}
}

func TestSettingsGettersPreserveUnconfiguredStrings(t *testing.T) {
	for _, tc := range []struct {
		code  byte
		wire  string
		field string
	}{
		{0x75, "3175010001327500003375000034750000", "ipAddress"},
		{0x77, "58750000367502000000397502000000", "domain"},
	} {
		result, _, err := settingsExecute(t, tc.code, `{}`, settingsHex(t, tc.wire))
		if err != nil {
			t.Fatalf("0x%02X unconfigured state rejected: %v", tc.code, err)
		}
		if settingsObject(t, result)[tc.field] != "" {
			t.Fatalf("0x%02X unconfigured value changed: %#v", tc.code, result)
		}
	}
}

func TestPrinterSettingsOptionalModelAndExtendedDictionary(t *testing.T) {
	params := `{"fontSize":0,"retract":1,"additionalText":""}`
	_, sent, err := settingsExecute(t, 0x78, params, nil)
	want := settingsHex(t, "3f75010000427501000141750000")
	if err != nil || !bytes.Equal(sent, want) {
		t.Fatalf("printer settings wire % X, error %v", sent, err)
	}
	for _, model := range []uint64{0, 7, 8, 9, 10} {
		params := fmt.Sprintf(`{"printerModel":%d,"fontSize":0,"retract":1,"additionalText":""}`, model)
		_, sent, err := settingsExecute(t, 0x78, params, nil)
		if err != nil || !bytes.Equal(sent[:5], []byte{0x43, 0x75, 1, 0, byte(model)}) {
			t.Fatalf("documented printer model %d rejected: % X, %v", model, sent, err)
		}
		result, _, err := settingsExecute(t, 0x79, `{}`, sent)
		if err != nil || settingsObject(t, result)["printerModel"] != float64(model) {
			t.Fatalf("printer model read %d: %#v, %v", model, result, err)
		}
	}
	for _, params := range []string{
		`{"printerModel":11,"fontSize":0,"retract":1,"additionalText":""}`,
		`{"fontSize":2,"retract":1,"additionalText":""}`,
		`{"fontSize":0,"retract":2,"additionalText":""}`,
		`{"fontSize":0,"retract":1}`,
		`{"fontSize":0,"additionalText":""}`,
	} {
		if err := settingsCommand(t, 0x78).Validate(json.RawMessage(params)); err == nil {
			t.Errorf("accepted invalid printer settings %s", params)
		}
	}
}

func TestPrinterSettingsCP866EncodedByteBoundary(t *testing.T) {
	params := `{"fontSize":0,"retract":0,"additionalText":"` + strings.Repeat("Я", 128) + `"}`
	_, sent, err := settingsExecute(t, 0x78, params, nil)
	if err != nil {
		t.Fatalf("128 CP866 bytes (256 UTF-8 bytes) rejected: %v", err)
	}
	wantPrefix := settingsHex(t, "3f75010000427501000041758000")
	if !bytes.Equal(sent, append(wantPrefix, bytes.Repeat([]byte{0x9F}, 128)...)) {
		t.Fatalf("incorrect CP866 text bytes % X", sent)
	}
	for _, value := range []string{strings.Repeat("Я", 129), "😀"} {
		params := `{"fontSize":0,"retract":0,"additionalText":"` + value + `"}`
		if err := settingsCommand(t, 0x78).Validate(json.RawMessage(params)); err == nil {
			t.Errorf("accepted unencodable/oversized receipt text %q", value)
		}
	}
}

func TestControlSettingsGoldenTLVsAndValues(t *testing.T) {
	wire := settingsHex(t, "4e75010001507502003412")
	_, sent, err := settingsExecute(t, 0x80, `{"controlInterface":1,"tcpPort":4660}`, nil)
	if err != nil || !bytes.Equal(sent, wire) {
		t.Fatalf("control settings wire % X, error %v", sent, err)
	}
	result, _, err := settingsExecute(t, 0x81, `{}`, wire)
	if err != nil {
		t.Fatal(err)
	}
	object := settingsObject(t, result)
	if object["controlInterface"] != float64(1) || object["tcpPort"] != float64(4660) {
		t.Fatalf("control settings result %#v", object)
	}
	for _, params := range []string{`{"controlInterface":2,"tcpPort":1}`, `{"controlInterface":0,"tcpPort":65536}`, `{"controlInterface":0}`, `{"tcpPort":1}`} {
		if err := settingsCommand(t, 0x80).Validate(json.RawMessage(params)); err == nil {
			t.Errorf("accepted invalid control settings %s", params)
		}
	}
	for _, port := range []uint64{0, 65535} {
		if err := settingsCommand(t, 0x80).Validate(json.RawMessage(fmt.Sprintf(`{"controlInterface":0,"tcpPort":%d}`, port))); err != nil {
			t.Fatalf("documented UINT16 port %d rejected: %v", port, err)
		}
	}
}

func TestSettingsGettersRejectMissingDuplicateAndWrongSizeTLVs(t *testing.T) {
	cases := []struct {
		code byte
		wire string
	}{
		{0x75, "31750200000032750c003139322e3136382e312e313033750d003235352e3235352e3235352e3034750b003139322e3136382e312e31"},
		{0x77, "58750b006f66642e6578616d706c653675010061397502001e00"},
		{0x79, "4375020000003f75010000427501000041750000"},
		{0x81, "4e750100005075010001"},
		{0x81, "4e750100004e75010000507502000100"},
		{0x81, "4e75010000"},
	}
	for _, tc := range cases {
		if _, _, err := settingsExecute(t, tc.code, `{}`, settingsHex(t, tc.wire)); err == nil {
			t.Errorf("0x%02X accepted malformed settings response %s", tc.code, tc.wire)
		}
	}
	for _, code := range []byte{0x75, 0x77, 0x79, 0x81} {
		if err := settingsCommand(t, code).Validate(json.RawMessage(`{"unknown":1}`)); err == nil {
			t.Errorf("0x%02X getter accepted input parameters", code)
		}
	}
}

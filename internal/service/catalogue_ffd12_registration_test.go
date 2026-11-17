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

func ffd12RegistrationTestCommand(t *testing.T, code byte) Command {
	t.Helper()
	for _, command := range ffd12RegistrationCommands() {
		if command.Definition.ProtocolCode == fmt.Sprintf("0x%02X", code) {
			return command
		}
	}
	t.Fatalf("registration command 0x%02X not found", code)
	return Command{}
}

func ffd12RegistrationDataParams() map[string]any {
	return map[string]any{
		"userName": "Тест", "settlementAddress": "A", "settlementPlace": "P", "representativeName": "R",
		"ofdInn": "7707083893", "ofdName": "OFD", "senderEmail": "Sales@example.test",
	}
}

func ffd12RegistrationCompletionParams() map[string]any {
	return map[string]any{"userInn": "7707083893", "registrationNumber": "0000000001007378", "taxationSystems": 33}
}

func registrationRaw(t *testing.T, params map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func ffd12RegistrationExecute(t *testing.T, code byte, raw json.RawMessage, response []byte) (any, []byte, error) {
	t.Helper()
	command := ffd12RegistrationTestCommand(t, code)
	if err := command.Validate(raw); err != nil {
		return nil, nil, err
	}
	var sent []byte
	device := testDevice("registration", func(_ context.Context, actual byte, payload []byte) ([]byte, error) {
		if actual != code {
			t.Fatalf("sent 0x%02X, want 0x%02X", actual, code)
		}
		sent = append([]byte{}, payload...)
		return response, nil
	}, 1)
	result, err := command.Execute(context.Background(), device, raw)
	return result, sent, err
}

func TestFFD12RegistrationWireDefinitionsAndSource(t *testing.T) {
	if len(ffd12RegistrationCommands()) != 3 {
		t.Fatal("expected exactly E1/E2/E3 wire definitions")
	}
	for _, command := range ffd12RegistrationCommands() {
		definition := command.Definition
		if !definition.Implemented || definition.ReadOnly || !definition.IdempotencyRequired || !definition.OperationRequired {
			t.Fatalf("wrong registration side-effect policy: %+v", definition)
		}
		if definition.Source != ffd12ExtraSource(definition.Source.Page) || definition.TitleRU == "" || definition.DescriptionRU == "" {
			t.Fatalf("missing registration supplement provenance: %+v", definition)
		}
	}
	e2 := ffd12RegistrationTestCommand(t, 0xE2).Definition
	for _, field := range e2.Input {
		if field.Tag == 1203 || field.Tag == 1036 || field.Tag == 1274 || field.Tag == 1275 || field.Tag == 9998 || field.Tag == 9999 {
			if field.Required {
				t.Fatalf("user-specified optional tag %d marked required", field.Tag)
			}
		}
	}
	e3 := ffd12RegistrationTestCommand(t, 0xE3).Definition
	if e3.Input[3].WireType != "UINT32" || e3.Input[3].Required || e3.Input[3].Bits["31"] != "Other reasons" {
		t.Fatalf("reason must be optional UINT32 bitmask: %+v", e3.Input[3])
	}
}

func TestFFD12RegistrationStartOneByte(t *testing.T) {
	for _, reportType := range []int{0, 1, 2} {
		_, payload, err := ffd12RegistrationExecute(t, 0xE1, registrationRaw(t, map[string]any{"reportType": reportType}), nil)
		if err != nil || !bytes.Equal(payload, []byte{byte(reportType)}) {
			t.Fatalf("reportType %d payload % X, error %v", reportType, payload, err)
		}
	}
	for _, raw := range []string{`{}`, `{"reportType":3}`, `{"reportType":-1}`, `{"reportType":1.5}`, `{"reportType":"1"}`, `{"reportType":null}`, `{"reportType":0,"extra":0}`} {
		if err := ffd12RegistrationTestCommand(t, 0xE1).Validate(json.RawMessage(raw)); err == nil {
			t.Errorf("accepted invalid registration start %s", raw)
		}
	}
}

func TestFFD12RegistrationDataGoldenTLVAndOptionalPresence(t *testing.T) {
	params := ffd12RegistrationDataParams()
	_, payload, err := ffd12RegistrationExecute(t, 0xE2, registrationRaw(t, params), nil)
	golden := settingsHex(t, "1804040092a5e1e2f103010041a304010050fd03010052f9030c00373730373038333839332020160403004f46445d04120053616c6573406578616d706c652e74657374")
	if err != nil || !bytes.Equal(payload, golden) {
		t.Fatalf("E2 wire % X, want % X, error %v", payload, golden, err)
	}
	params["representativeInn"] = "500100732259"
	params["machineNumber"] = "M1"
	params["reportAttribute"] = "A"
	params["reportDataHex"] = "00fF"
	params["applicationFlags"] = 0
	params["operatingMode"] = 0
	_, payload, err = ffd12RegistrationExecute(t, 0xE2, registrationRaw(t, params), nil)
	if err != nil {
		t.Fatal(err)
	}
	tags, err := ParseTLV(payload)
	if err != nil {
		t.Fatal(err)
	}
	wantTags := []uint16{1048, 1009, 1187, 1021, 1203, 1017, 1046, 1117, 1036, 1274, 1275, 9998, 9999}
	if len(tags) != len(wantTags) {
		t.Fatalf("optional fields missing: %+v", tags)
	}
	for i, tag := range tags {
		if tag.Tag != wantTags[i] {
			t.Fatalf("tag order %d = %d, want %d", i, tag.Tag, wantTags[i])
		}
		if (tag.Tag == 9998 || tag.Tag == 9999) && !bytes.Equal(tag.Value, []byte{0}) {
			t.Fatalf("explicit zero flags were not encoded: %+v", tag)
		}
		if tag.Tag == 1275 && !bytes.Equal(tag.Value, []byte{0, 0xFF}) {
			t.Fatalf("report hex was not decoded: %+v", tag)
		}
	}
}

func TestFFD12RegistrationAutonomousModeOmitsValidatedOfdTags(t *testing.T) {
	params := ffd12RegistrationDataParams()
	params["operatingMode"] = 2
	for _, supplied := range []bool{true, false} {
		if !supplied {
			delete(params, "ofdInn")
			delete(params, "ofdName")
			delete(params, "senderEmail")
		}
		_, payload, err := ffd12RegistrationExecute(t, 0xE2, registrationRaw(t, params), nil)
		if err != nil {
			t.Fatal(err)
		}
		tags, err := ParseTLV(payload)
		if err != nil {
			t.Fatal(err)
		}
		for _, tag := range tags {
			if tag.Tag == 1017 || tag.Tag == 1046 || tag.Tag == 1117 {
				t.Fatalf("autonomous OFD tag %d was transmitted", tag.Tag)
			}
		}
	}
	params["ofdInn"] = "7707083894"
	if err := ffd12RegistrationTestCommand(t, 0xE2).Validate(registrationRaw(t, params)); err == nil {
		t.Fatal("autonomous mode bypassed validation of supplied OFD INN")
	}
	delete(params, "ofdInn")
	params["senderEmail"] = "not-email"
	if err := ffd12RegistrationTestCommand(t, 0xE2).Validate(registrationRaw(t, params)); err == nil {
		t.Fatal("autonomous mode bypassed validation of supplied sender email")
	}
}

func TestFFD12RegistrationDataRejectsMissingOrInvalidParameters(t *testing.T) {
	command := ffd12RegistrationTestCommand(t, 0xE2)
	for _, field := range []string{"userName", "settlementAddress", "settlementPlace", "representativeName", "ofdInn", "ofdName", "senderEmail"} {
		params := ffd12RegistrationDataParams()
		delete(params, field)
		if err := command.Validate(registrationRaw(t, params)); err == nil {
			t.Errorf("required field %s was inferred", field)
		}
	}
	for field, values := range map[string][]any{
		"representativeInn": {"7707083893", "500100732258", nil, 500100732259},
		"ofdInn":            {"7707083894", "0000000000", "7707083893  ", nil},
		"senderEmail":       {"Name <a@example.test>", "a@@example.test", "a@example.test,b@example.test", "a@example.test\n", nil},
		"applicationFlags":  {128, 256, -1, 1.5, "0", nil},
		"operatingMode":     {4, 64, 3, 34, 16, -1, "2", nil},
		"reportAttribute":   {"unpaired"},
		"reportDataHex":     {"00", "odd", nil},
		"unknown":           {1},
	} {
		for _, value := range values {
			params := ffd12RegistrationDataParams()
			params[field] = value
			if err := command.Validate(registrationRaw(t, params)); err == nil {
				t.Errorf("accepted invalid %s=%v", field, value)
			}
		}
	}
	for _, mode := range []int{0, 1, 8, 9, 24, 25, 32, 40, 56} {
		params := ffd12RegistrationDataParams()
		params["operatingMode"] = mode
		params["applicationFlags"] = 127
		if err := command.Validate(registrationRaw(t, params)); err != nil {
			t.Errorf("valid masks rejected (mode=%d): %v", mode, err)
		}
	}
}

func TestFFD12RegistrationEncodedLengthsAndReportPair(t *testing.T) {
	command := ffd12RegistrationTestCommand(t, 0xE2)
	for field, max := range map[string]int{"userName": 128, "settlementAddress": 256, "settlementPlace": 256, "representativeName": 64, "ofdName": 64, "machineNumber": 21} {
		params := ffd12RegistrationDataParams()
		params[field] = strings.Repeat("Я", max)
		if err := command.Validate(registrationRaw(t, params)); err != nil {
			t.Errorf("%s rejected %d CP866 bytes: %v", field, max, err)
		}
		params[field] = strings.Repeat("Я", max+1)
		if err := command.Validate(registrationRaw(t, params)); err == nil {
			t.Errorf("%s accepted %d CP866 bytes", field, max+1)
		}
	}
	params := ffd12RegistrationDataParams()
	params["reportAttribute"] = strings.Repeat("Я", 32)
	params["reportDataHex"] = strings.Repeat("ff", 32)
	if err := command.Validate(registrationRaw(t, params)); err != nil {
		t.Fatal(err)
	}
	params["reportDataHex"] = strings.Repeat("ff", 33)
	if err := command.Validate(registrationRaw(t, params)); err == nil {
		t.Fatal("report data length measured as characters instead of decoded bytes")
	}
	params["reportDataHex"] = ""
	params["reportAttribute"] = strings.Repeat("Я", 33)
	if err := command.Validate(registrationRaw(t, params)); err == nil {
		t.Fatal("accepted overlong report attribute")
	}
	params = ffd12RegistrationDataParams()
	params["userName"] = "😀"
	if err := command.Validate(registrationRaw(t, params)); err == nil {
		t.Fatal("accepted text unavailable in CP866")
	}
	params = ffd12RegistrationDataParams()
	params["senderEmail"] = strings.Repeat("a", 51) + "@example.test"
	if err := command.Validate(registrationRaw(t, params)); err != nil {
		t.Fatalf("64-byte sender email rejected: %v", err)
	}
	params["senderEmail"] = strings.Repeat("a", 52) + "@example.test"
	if err := command.Validate(registrationRaw(t, params)); err == nil {
		t.Fatal("65-byte sender email accepted")
	}
}

func TestFFD12RegistrationCompletionSpacePaddingAndFourByteReason(t *testing.T) {
	params := ffd12RegistrationCompletionParams()
	response := []byte{4, 3, 2, 1, 8, 7, 6, 5}
	result, payload, err := ffd12RegistrationExecute(t, 0xE3, registrationRaw(t, params), response)
	want := append([]byte("7707083893  0000000001007378    "), 33)
	if err != nil || !bytes.Equal(payload, want) || len(payload) != 33 {
		t.Fatalf("initial E3 sent % X, want % X, error %v", payload, want, err)
	}
	registrationTestJSON(t, result, `{"documentNumber":16909060,"fiscalSign":84281096}`)
	params["reregistrationReason"] = uint32(0x80000002)
	_, payload, err = ffd12RegistrationExecute(t, 0xE3, registrationRaw(t, params), response)
	want = append(want, 2, 0, 0, 128)
	if err != nil || !bytes.Equal(payload, want) || len(payload) != 37 {
		t.Fatalf("re-registration E3 sent % X, want % X, error %v", payload, want, err)
	}
	params["userInn"] = "500100732259"
	params["registrationNumber"] = "1234567890049807"
	_, payload, err = ffd12RegistrationExecute(t, 0xE3, registrationRaw(t, params), response)
	if err != nil || string(payload[:12]) != "500100732259" {
		t.Fatalf("personal INN encoding % X, error %v", payload, err)
	}
}

func TestFFD12RegistrationCompletionRejectsInvalidValues(t *testing.T) {
	command := ffd12RegistrationTestCommand(t, 0xE3)
	for field, values := range map[string][]any{
		"userInn":              {"7707083894", "000000000000", "7707083893  ", nil, 7707083893},
		"registrationNumber":   {"0000000001007378    ", "000000000100737", "000000000100737X", nil},
		"taxationSystems":      {0, 8, 64, 255, -1, 1.5, "1", nil},
		"reregistrationReason": {0, 1 << 15, 1 << 16, 1 << 22, 1 << 30, uint64(1) << 32, -1, 1.5, "2147483648", nil},
		"extra":                {1},
	} {
		for _, value := range values {
			params := ffd12RegistrationCompletionParams()
			params[field] = value
			if err := command.Validate(registrationRaw(t, params)); err == nil {
				t.Errorf("accepted invalid %s=%v", field, value)
			}
		}
	}
	for _, reason := range []uint64{1, 31, 1 << 17, 1 << 21, 1 << 31, ffd12RegistrationReasonMask} {
		params := ffd12RegistrationCompletionParams()
		params["reregistrationReason"] = reason
		params["taxationSystems"] = 0x37
		if err := command.Validate(registrationRaw(t, params)); err != nil {
			t.Errorf("valid reason %d rejected: %v", reason, err)
		}
	}
}

func TestFFD12RegistrationResponsesRequireExactDocumentLayout(t *testing.T) {
	for _, tc := range []struct {
		code    byte
		raw     json.RawMessage
		lengths []int
	}{
		{0xE1, json.RawMessage(`{"reportType":0}`), []int{1}},
		{0xE2, registrationRaw(t, ffd12RegistrationDataParams()), []int{1}},
		{0xE3, registrationRaw(t, ffd12RegistrationCompletionParams()), []int{0, 7, 9}},
	} {
		for _, length := range tc.lengths {
			_, _, err := ffd12RegistrationExecute(t, tc.code, tc.raw, make([]byte, length))
			if !errors.Is(err, protocol.ErrMalformedFrame) {
				t.Errorf("0x%02X accepted %d-byte response: %v", tc.code, length, err)
			}
		}
	}
}

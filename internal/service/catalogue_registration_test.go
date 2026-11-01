package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	terminalfa "github.com/dronm/goterminalfa"
)

func registrationTestCommand(t *testing.T, code byte) Command {
	t.Helper()
	for _, cmd := range registrationCommands() {
		if strings.EqualFold(cmd.Definition.ProtocolCode, fmt.Sprintf("0x%02X", code)) {
			return cmd
		}
	}
	t.Fatalf("missing registration command 0x%02X", code)
	return Command{}
}

func registrationTestExecute(t *testing.T, cmd Command, params string, expected []byte, response []byte) any {
	t.Helper()
	if err := cmd.Validate(json.RawMessage(params)); err != nil {
		t.Fatalf("validate %s: %v", cmd.Definition.Name, err)
	}
	calls := 0
	device := NewDevice("registration-test", "serial", "", terminalfa.NewClient(exchangeFunc(func(_ context.Context, code byte, payload []byte) ([]byte, error) {
		calls++
		if !strings.EqualFold(cmd.Definition.ProtocolCode, fmt.Sprintf("0x%02X", code)) {
			t.Fatalf("wire code 0x%02X for %s", code, cmd.Definition.ProtocolCode)
		}
		if !bytes.Equal(payload, expected) {
			t.Fatalf("wire payload % X; want % X", payload, expected)
		}
		return response, nil
	})), 1)
	value, err := cmd.Execute(context.Background(), device, json.RawMessage(params))
	if err != nil {
		t.Fatalf("execute %s: %v", cmd.Definition.Name, err)
	}
	if calls != 1 {
		t.Fatalf("executed %d exchanges; want one", calls)
	}
	return value
}

func registrationTestJSON(t *testing.T, value any, expected string) {
	t.Helper()
	actual, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(actual, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatal(err)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("response %s; want %s", gotJSON, wantJSON)
	}
}

func TestRegistrationCatalogueCoverageAndPolicy(t *testing.T) {
	for _, code := range []byte{0x10, 0x12, 0x13, 0x16, 0x20, 0x21, 0x22, 0x29, 0x2A, 0x2F} {
		cmd := registrationTestCommand(t, code)
		if cmd.Definition.ReadOnly != (code == 0x20) || cmd.Definition.IdempotencyRequired != (code != 0x20) {
			t.Fatalf("incorrect side-effect policy for 0x%02X: %+v", code, cmd.Definition)
		}
		if cmd.Definition.TitleRU == "" || cmd.Definition.DescriptionRU == "" || cmd.Definition.Source.Page == 0 {
			t.Fatalf("documentation absent for 0x%02X", code)
		}
	}
	if len(registrationCommands()) != 10 {
		t.Fatalf("unexpected registration catalogue size %d", len(registrationCommands()))
	}
}

func TestRegistrationReportTypeIsOneRequiredByte(t *testing.T) {
	cmd := registrationTestCommand(t, 0x12)
	for _, reportType := range []int{0, 1, 2} {
		registrationTestExecute(t, cmd, fmt.Sprintf(`{"reportType":%d}`, reportType), []byte{byte(reportType)}, nil)
	}
	for _, params := range []string{`{}`, `{"reportType":3}`, `{"reportType":-1}`, `{"reportType":1.5}`, `{"reportType":"1"}`, `{"reportType":0,"extra":1}`} {
		if err := cmd.Validate(json.RawMessage(params)); err == nil {
			t.Errorf("accepted invalid report type request %s", params)
		}
	}
}

func TestRegistrationCompletionFixedPaddingOptionalReasonAndFiscalResult(t *testing.T) {
	cmd := registrationTestCommand(t, 0x13)
	base := []byte("1234567890  12345678901234567890")
	base = append(base, 0x21)
	response := []byte{0x78, 0x56, 0x34, 0x12, 0xEF, 0xCD, 0xAB, 0x09}
	params := `{"userInn":"1234567890","registrationNumber":"12345678901234567890","taxationSystems":33}`
	value := registrationTestExecute(t, cmd, params, base, response)
	registrationTestJSON(t, value, `{"documentNumber":305419896,"fiscalSign":162254319}`)
	withReason := strings.TrimSuffix(params, "}") + `,"reregistrationReason":2}`
	registrationTestExecute(t, cmd, withReason, append(append([]byte{}, base...), 2), response)
	for _, invalid := range []string{
		`{"userInn":"12345678901","registrationNumber":"12345678901234567890","taxationSystems":1}`,
		`{"userInn":"123456789X","registrationNumber":"12345678901234567890","taxationSystems":1}`,
		`{"userInn":"1234567890","registrationNumber":"1234567890123456789","taxationSystems":1}`,
		`{"userInn":"1234567890","registrationNumber":"12345678901234567890","taxationSystems":1,"reregistrationReason":0}`,
	} {
		if err := cmd.Validate(json.RawMessage(invalid)); err == nil {
			t.Errorf("accepted invalid completion request %s", invalid)
		}
	}
}

func TestShiftOptionalPrintBytePreservesOmissionAndZero(t *testing.T) {
	for _, code := range []byte{0x21, 0x29} {
		cmd := registrationTestCommand(t, code)
		registrationTestExecute(t, cmd, `{}`, nil, nil)
		registrationTestExecute(t, cmd, `{"suppressPrint":0}`, []byte{0}, nil)
		registrationTestExecute(t, cmd, `{"suppressPrint":1}`, []byte{1}, nil)
		if err := cmd.Validate(json.RawMessage(`{"suppressPrint":2}`)); err == nil {
			t.Fatalf("accepted invalid print flag for 0x%02X", code)
		}
	}
}

func TestShiftCompletionAndReadResponseLayouts(t *testing.T) {
	for _, code := range []byte{0x22, 0x2A} {
		cmd := registrationTestCommand(t, code)
		value := registrationTestExecute(t, cmd, `{}`, nil, []byte{0x34, 0x12, 4, 3, 2, 1, 8, 7, 6, 5})
		registrationTestJSON(t, value, `{"shiftNumber":4660,"documentNumber":16909060,"fiscalSign":84281096}`)
	}
	cmd := registrationTestCommand(t, 0x20)
	value := registrationTestExecute(t, cmd, `{}`, nil, []byte{1, 0x34, 0x12, 0x78, 0x56})
	registrationTestJSON(t, value, `{"shiftState":1,"shiftNumber":4660,"receiptNumber":22136}`)
}

func TestCashierDataTLVEncodingAndOptionalInn(t *testing.T) {
	cmd := registrationTestCommand(t, 0x2F)
	name := []byte{0xFD, 0x03, 4, 0, 'I', 'v', 'a', 'n'}
	registrationTestExecute(t, cmd, `{"cashierName":"Ivan"}`, name, nil)
	withInn := append(append([]byte{}, name...), []byte{0xB3, 0x04, 12, 0}...)
	withInn = append(withInn, []byte("123456789012")...)
	registrationTestExecute(t, cmd, `{"cashierName":"Ivan","cashierInn":"123456789012"}`, withInn, nil)
	for _, params := range []string{`{}`, `{"cashierName":"Ivan","cashierInn":"1234567890"}`, `{"cashierName":"Ivan","cashierInn":"12345678901X"}`, `{"cashierName":"Ivan","unknown":true}`} {
		if err := cmd.Validate(json.RawMessage(params)); err == nil {
			t.Errorf("accepted invalid cashier request %s", params)
		}
	}
}

func TestRegistrationDataAutonomousAndOfdConditionalValidation(t *testing.T) {
	cmd := registrationTestCommand(t, 0x16)
	autonomous := `{"userName":"ACME","representativeName":"Ivan","representativeInn":"123456789012","agentFlags":0,"operatingMode":2}`
	if err := cmd.Validate(json.RawMessage(autonomous)); err != nil {
		t.Fatalf("valid autonomous request rejected: %v", err)
	}
	onlineMissingOFD := strings.Replace(autonomous, `"operatingMode":2`, `"operatingMode":0`, 1)
	if err := cmd.Validate(json.RawMessage(onlineMissingOFD)); err == nil {
		t.Fatal("accepted nonautonomous registration without OFD data")
	}
	online := strings.TrimSuffix(onlineMissingOFD, "}") + `,"ofdInn":"1234567890","ofdName":"OFD","senderEmail":"a@example.test"}`
	if err := cmd.Validate(json.RawMessage(online)); err != nil {
		t.Fatalf("valid OFD data rejected: %v", err)
	}
	if err := cmd.Validate(json.RawMessage(strings.Replace(autonomous, `123456789012`, `12345678901X`, 1))); err == nil {
		t.Fatal("accepted nonnumeric representative INN")
	}
	want := []byte{}
	for _, item := range []struct {
		tag  uint16
		data []byte
	}{
		{1048, []byte("ACME")}, {1021, []byte("Ivan")}, {1203, []byte("123456789012")}, {1057, []byte{0}}, {9999, []byte{2}},
	} {
		header := make([]byte, 4)
		binary.LittleEndian.PutUint16(header, item.tag)
		binary.LittleEndian.PutUint16(header[2:], uint16(len(item.data)))
		want = append(want, header...)
		want = append(want, item.data...)
	}
	registrationTestExecute(t, cmd, autonomous, want, nil)
	// The OFD INN is a twelve-byte padded TLV value even when its logical
	// taxpayer identifier has ten digits. Preserve the documented field order.
	want = nil
	for _, item := range []struct {
		tag  uint16
		data []byte
	}{
		{1048, []byte("ACME")}, {1021, []byte("Ivan")}, {1203, []byte("123456789012")},
		{1017, []byte("1234567890  ")}, {1046, []byte("OFD")}, {1117, []byte("a@example.test")},
		{1057, []byte{0}}, {9999, []byte{0}},
	} {
		header := make([]byte, 4)
		binary.LittleEndian.PutUint16(header, item.tag)
		binary.LittleEndian.PutUint16(header[2:], uint16(len(item.data)))
		want = append(want, header...)
		want = append(want, item.data...)
	}
	registrationTestExecute(t, cmd, online, want, nil)
}

func TestRegistrationCommandsRejectMalformedSuccessPayloads(t *testing.T) {
	for _, code := range []byte{0x10, 0x12, 0x13, 0x16, 0x20, 0x21, 0x22, 0x29, 0x2A, 0x2F} {
		cmd := registrationTestCommand(t, code)
		params := `{}`
		switch code {
		case 0x12:
			params = `{"reportType":0}`
		case 0x13:
			params = `{"userInn":"1234567890","registrationNumber":"12345678901234567890","taxationSystems":1}`
		case 0x16:
			params = `{"userName":"ACME","representativeName":"Ivan","representativeInn":"123456789012","agentFlags":0,"operatingMode":2}`
		case 0x2F:
			params = `{"cashierName":"Ivan"}`
		}
		device := NewDevice("malformed", "serial", "", terminalfa.NewClient(exchangeFunc(func(context.Context, byte, []byte) ([]byte, error) { return []byte{0xFF}, nil })), 1)
		if _, err := cmd.Execute(context.Background(), device, json.RawMessage(params)); err == nil {
			t.Errorf("0x%02X accepted malformed success response", code)
		}
	}
}

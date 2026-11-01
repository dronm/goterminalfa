package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	terminalfa "github.com/dronm/goterminalfa"
)

type archiveExchangeFunc func(context.Context, byte, []byte) ([]byte, error)

func (f archiveExchangeFunc) Exchange(ctx context.Context, command byte, payload []byte) ([]byte, error) {
	return f(ctx, command, payload)
}

func archiveCommand(t *testing.T, code byte) Command {
	t.Helper()
	for _, command := range archivePrintCommands() {
		if command.Definition.ProtocolCode == fmt.Sprintf("0x%02X", code) {
			return command
		}
	}
	t.Fatalf("archive/printer command 0x%02X is missing", code)
	return Command{}
}

func archiveExecute(t *testing.T, code byte, params string, response []byte) (any, []byte, error) {
	t.Helper()
	command := archiveCommand(t, code)
	if err := command.Validate(json.RawMessage(params)); err != nil {
		return nil, nil, err
	}
	var sent []byte
	device := &Device{Client: terminalfa.NewClient(archiveExchangeFunc(func(_ context.Context, actual byte, payload []byte) ([]byte, error) {
		if actual != code {
			t.Fatalf("sent 0x%02X, wanted 0x%02X", actual, code)
		}
		sent = append([]byte{}, payload...)
		return response, nil
	}))}
	result, err := command.Execute(context.Background(), device, json.RawMessage(params))
	return result, sent, err
}

func archiveResult(t *testing.T, result any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func TestArchivePrinterCatalogueCoverageAndPolicy(t *testing.T) {
	codes := []byte{0x30, 0x31, 0x32, 0x33, 0x35, 0x36, 0x39, 0x3A, 0x3B, 0x40, 0x61, 0x62, 0x63, 0x64, 0x65, 0x69, 0x72, 0x73}
	readOnly := map[byte]bool{0x30: true, 0x32: true, 0x33: true, 0x3A: true, 0x3B: true, 0x73: true}
	if len(archivePrintCommands()) != len(codes) {
		t.Fatalf("got %d definitions", len(archivePrintCommands()))
	}
	for _, code := range codes {
		command := archiveCommand(t, code)
		if command.Definition.ReadOnly != readOnly[code] || command.Definition.IdempotencyRequired == readOnly[code] {
			t.Errorf("0x%02X policy incorrect", code)
		}
		if command.Definition.TitleRU == "" || command.Definition.DescriptionRU == "" || command.Definition.Source.Page == 0 {
			t.Errorf("0x%02X lacks source documentation", code)
		}
	}
}

func TestArchiveFixedRequestsAndResponses(t *testing.T) {
	result, sent, err := archiveExecute(t, 0x30, `{"documentNumber":16909060}`, []byte{3, 1, 0xA0, 0xB1})
	if err != nil || !bytes.Equal(sent, []byte{4, 3, 2, 1}) {
		t.Fatalf("find bytes % X, error %v", sent, err)
	}
	object := archiveResult(t, result)
	if object["documentType"] != float64(3) || object["ofdAcknowledged"] != float64(1) || object["documentData"] != "a0b1" {
		t.Fatalf("archive result %#v", object)
	}
	result, sent, err = archiveExecute(t, 0x32, `{}`, []byte{0x34, 0x12})
	if err != nil || len(sent) != 0 || archiveResult(t, result)["count"] != float64(0x1234) {
		t.Fatalf("count %#v, % X, %v", result, sent, err)
	}
	result, sent, err = archiveExecute(t, 0x35, `{"documentNumber":1}`, []byte{3, 0, 0x34, 0x12})
	if err != nil || !bytes.Equal(sent, []byte{1, 0, 0, 0}) || archiveResult(t, result)["documentLength"] != float64(0x1234) {
		t.Fatalf("select %#v, % X, %v", result, sent, err)
	}
	for _, code := range []byte{0x30, 0x31, 0x35, 0x39, 0x3A} {
		if err := archiveCommand(t, code).Validate(json.RawMessage(`{"documentNumber":4294967296}`)); err == nil {
			t.Errorf("0x%02X accepted UINT32 overflow", code)
		}
	}
}

func TestArchiveAcknowledgementUses18ByteSign(t *testing.T) {
	response := append([]byte{26, 10, 30, 11, 45}, bytes.Repeat([]byte{0xAB}, 18)...)
	response = append(response, 4, 3, 2, 1)
	result, sent, err := archiveExecute(t, 0x31, `{"documentNumber":16909060}`, response)
	if err != nil || !bytes.Equal(sent, []byte{4, 3, 2, 1}) {
		t.Fatalf("ack %#v, % X, %v", result, sent, err)
	}
	object := archiveResult(t, result)
	if object["acknowledgedAt"] != "2026-10-30T11:45" || object["ofdFiscalSign"] != strings.Repeat("ab", 18) || object["documentNumber"] != float64(16909060) {
		t.Fatalf("ack %#v", object)
	}
	if _, _, err := archiveExecute(t, 0x31, `{"documentNumber":1}`, response[:26]); err == nil {
		t.Fatal("accepted truncated acknowledgement")
	}
}

func activationResponse(reason bool) []byte {
	response := []byte{26, 10, 30, 11, 45}
	response = append(response, []byte("1234567890  ")...)
	response = append(response, []byte("12345678901234567890")...)
	response = append(response, 1, 4)
	if reason {
		response = append(response, 3)
	}
	response = binary.LittleEndian.AppendUint32(response, 0x01020304)
	return binary.LittleEndian.AppendUint32(response, 0x11223344)
}

func TestActivationTotalsConditionalReasonByte(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		params := `{}`
		want := []byte{}
		if indexed {
			params = `{"registrationReportNumber":2}`
			want = []byte{2}
		}
		result, sent, err := archiveExecute(t, 0x33, params, activationResponse(indexed))
		if err != nil || !bytes.Equal(sent, want) {
			t.Fatalf("indexed %t: % X, %v", indexed, sent, err)
		}
		object := archiveResult(t, result)
		if object["taxpayerINN"] != "1234567890" || object["documentNumber"] != float64(0x01020304) || object["fiscalSign"] != float64(0x11223344) {
			t.Fatalf("activation %#v", object)
		}
		_, present := object["reregistrationReason"]
		if present != indexed || (indexed && object["reregistrationReason"] != float64(3)) {
			t.Fatalf("reason %#v", object)
		}
	}
	for _, size := range []int{0, 46, 49} {
		if _, _, err := archiveExecute(t, 0x33, `{}`, make([]byte, size)); err == nil {
			t.Errorf("accepted activation length %d", size)
		}
	}
}

func TestArchiveTLVPreservesOrderRepetitionAndRawBytes(t *testing.T) {
	response := []byte{0xE8, 3, 1, 0, 0xA0, 0xE8, 3, 2, 0, 0xB1, 0xC2, 0xFE, 0xFF, 0, 0}
	for _, code := range []byte{0x36, 0x3A, 0x3B} {
		params := `{}`
		if code == 0x3A {
			params = `{"documentNumber":1}`
		} else if code == 0x3B {
			params = `{"registrationReportNumber":1}`
		}
		result, _, err := archiveExecute(t, code, params, response)
		if err != nil {
			t.Fatalf("0x%02X: %v", code, err)
		}
		object := archiveResult(t, result)
		values := object["tlv"].([]any)
		if len(values) != 3 || values[0].(map[string]any)["tag"] != float64(1000) || values[1].(map[string]any)["valueHex"] != "b1c2" || object["rawHex"] != hex.EncodeToString(response) {
			t.Fatalf("lost TLV details: %#v", object)
		}
		if _, _, err := archiveExecute(t, code, params, response[:4]); err == nil {
			t.Errorf("0x%02X accepted truncated TLV", code)
		}
	}
}

func TestPrintFiscalDocumentIgnoresExtraSuccessfulData(t *testing.T) {
	_, sent, err := archiveExecute(t, 0x39, `{"documentNumber":1}`, []byte{0xA0, 0xB1, 0xC2})
	if err != nil || !bytes.Equal(sent, []byte{1, 0, 0, 0}) {
		t.Fatalf("bytes % X, error %v", sent, err)
	}
}

func TestPrinterStatusesAreReportedExplicitly(t *testing.T) {
	for _, tc := range []struct {
		code   byte
		params string
	}{
		{0x61, `{"fontAttributes":0,"alignment":0,"text":"Hello"}`},
		{0x62, `{}`},
		{0x63, `{"text":"123456"}`},
		{0x69, `{"barcodeType":0,"text":"1234567890123"}`},
	} {
		for _, response := range [][]byte{nil, {0}, {2}} {
			result, _, err := archiveExecute(t, tc.code, tc.params, response)
			if err != nil {
				t.Fatalf("0x%02X: %v", tc.code, err)
			}
			ready := len(response) == 0 || response[0] == 0
			if archiveResult(t, result)["printerReady"] != ready {
				t.Fatalf("0x%02X hid printer status: %#v", tc.code, result)
			}
		}
		if _, _, err := archiveExecute(t, tc.code, tc.params, []byte{1, 2}); err == nil {
			t.Errorf("0x%02X accepted extra printer bytes", tc.code)
		}
	}
}

func TestPrinterOptionalFlagAndCP866Text(t *testing.T) {
	_, sent, err := archiveExecute(t, 0x62, `{}`, nil)
	if err != nil || len(sent) != 0 {
		t.Fatalf("omission: % X, %v", sent, err)
	}
	_, sent, err = archiveExecute(t, 0x62, `{"partialCut":0}`, nil)
	if err != nil || !bytes.Equal(sent, []byte{0}) {
		t.Fatalf("explicit zero: % X, %v", sent, err)
	}
	_, sent, err = archiveExecute(t, 0x61, `{"fontAttributes":8,"alignment":1,"text":"Привет"}`, nil)
	if err != nil || !bytes.Equal(sent, []byte{8, 1, 0x8F, 0xE0, 0xA8, 0xA2, 0xA5, 0xE2}) {
		t.Fatalf("CP866: % X, %v", sent, err)
	}
	for _, tc := range []struct {
		code   byte
		params string
	}{
		{0x61, `{"fontAttributes":0,"alignment":3,"text":"Hello"}`},
		{0x61, `{"fontAttributes":0,"alignment":0,"text":"😀"}`},
		{0x62, `{"partialCut":2}`}, {0x63, `{"text":"12345"}`},
		{0x69, `{"barcodeType":0,"text":"12345678901234"}`},
		{0x69, `{"barcodeType":1,"text":"1234567890123456789012345"}`},
		{0x69, `{"barcodeType":2,"text":"123456"}`},
	} {
		if err := archiveCommand(t, tc.code).Validate(json.RawMessage(tc.params)); err == nil {
			t.Errorf("0x%02X accepted %s", tc.code, tc.params)
		}
	}
}

func TestFormattedBufferUsesRepeatedPositionalTLVs(t *testing.T) {
	params := `{"lines":[{"fontAttributes":0,"alignment":0,"text":"Hello"},{"fontAttributes":8,"alignment":1,"text":"1234567890"}]}`
	_, sent, err := archiveExecute(t, 0x64, params, nil)
	want, _ := hex.DecodeString("3f9c0700000048656c6c6f3f9c0c00080131323334353637383930")
	if err != nil || !bytes.Equal(sent, want) {
		t.Fatalf("buffer % X, %v", sent, err)
	}
	for _, params := range []string{`{"lines":[]}`, `{"lines":[{"fontAttributes":0,"alignment":0,"text":"` + strings.Repeat("x", 125) + `"}]}`, `{"lines":[{"fontAttributes":0,"alignment":0,"text":"ok","unknown":1}]}`} {
		if err := archiveCommand(t, 0x64).Validate(json.RawMessage(params)); err == nil {
			t.Errorf("accepted buffer %s", params)
		}
	}
}

func TestClockTLVAndFixedSimulatorReset(t *testing.T) {
	response := []byte{0x30, 0x75, 5, 0, 26, 10, 30, 11, 45}
	_, sent, err := archiveExecute(t, 0x72, `{"dateTime":"2026-10-30T11:45"}`, nil)
	if err != nil || !bytes.Equal(sent, response) {
		t.Fatalf("clock write % X, %v", sent, err)
	}
	result, sent, err := archiveExecute(t, 0x73, `{}`, response)
	if err != nil || len(sent) != 0 || archiveResult(t, result)["dateTime"] != "2026-10-30T11:45" {
		t.Fatalf("clock %#v, % X, %v", result, sent, err)
	}
	if err := archiveCommand(t, 0x72).Validate(json.RawMessage(`{"dateTime":"2026-02-30T11:45"}`)); err == nil {
		t.Fatal("accepted invalid calendar date")
	}
	_, sent, err = archiveExecute(t, 0x40, `{}`, nil)
	if err != nil || !bytes.Equal(sent, []byte{0x16}) {
		t.Fatalf("reset % X, %v", sent, err)
	}
	if err := archiveCommand(t, 0x40).Validate(json.RawMessage(`{"requestCode":1}`)); err == nil {
		t.Fatal("accepted caller-selected reset byte")
	}
}

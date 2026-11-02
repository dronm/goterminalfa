package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func ffd12ExtraTestCommand(t *testing.T, name string) Command {
	t.Helper()
	base := append(registrationCommands(), receiptCommands()...)
	for _, command := range ffd12ExtraCommands(base) {
		if command.Definition.Name == name {
			return command
		}
	}
	t.Fatalf("extra command %s not found", name)
	return Command{}
}

func ffd12ExtraTestPayload(t *testing.T, name, params string, response []byte) ([]byte, any, error) {
	t.Helper()
	command := ffd12ExtraTestCommand(t, name)
	if err := command.Validate(json.RawMessage(params)); err != nil {
		return nil, nil, err
	}
	var payload []byte
	device := testDevice("extra", func(_ context.Context, code byte, data []byte) ([]byte, error) {
		want := byte(0x98)
		if name == "sendSettlementReportData" {
			want = 0xE6
		}
		if code != want {
			t.Fatalf("wire code %02X, want %02X", code, want)
		}
		payload = append([]byte{}, data...)
		return response, nil
	}, 1)
	result, err := command.Execute(context.Background(), device, json.RawMessage(params))
	return payload, result, err
}

func TestFFD12ExtraSettlementDataGoldenAndPairedFields(t *testing.T) {
	payload, _, err := ffd12ExtraTestPayload(t, "sendSettlementReportData", `{"documentAddress":"A","documentPlace":"P","reportAttribute":"R","reportDataHex":"00ff"}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := hex.DecodeString("f103010041a30401005000050100520105020000ff")
	if !bytes.Equal(payload, want) {
		t.Fatalf("got % X, want % X", payload, want)
	}
	for _, params := range []string{
		`{}`, `{"documentAddress":"A"}`,
		`{"documentAddress":"A","documentPlace":"P","reportAttribute":"R"}`,
		`{"documentAddress":"A","documentPlace":"P","reportDataHex":"00"}`,
		`{"documentAddress":"A","documentPlace":"P","reportAttribute":"R","reportDataHex":"xyz"}`,
	} {
		if err := ffd12ExtraTestCommand(t, "sendSettlementReportData").Validate(json.RawMessage(params)); err == nil {
			t.Fatalf("accepted invalid report params %s", params)
		}
	}
	for _, values := range []map[string]any{
		{"documentAddress": strings.Repeat("A", 257), "documentPlace": "P"},
		{"documentAddress": "A", "documentPlace": "P", "reportAttribute": strings.Repeat("R", 33), "reportDataHex": "00"},
		{"documentAddress": "A", "documentPlace": "P", "reportAttribute": "R", "reportDataHex": strings.Repeat("00", 33)},
	} {
		data, _ := json.Marshal(values)
		if err := ffd12ExtraTestCommand(t, "sendSettlementReportData").Validate(data); err == nil {
			t.Fatalf("accepted report bounds violation %s", data)
		}
	}
}

func TestFFD12ExtraCorrectionDateIsDirectUnixTimeTag(t *testing.T) {
	payload, _, err := ffd12ExtraTestPayload(t, "setReceiptAdditionalData", `{"correctedCalculationDate":1704067200,"prescriptionNumber":"AB"}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := hex.DecodeString("9a040400800092659b0402004142")
	if !bytes.Equal(payload, want) {
		t.Fatalf("got % X, want direct 1178/1179 tags % X", payload, want)
	}
	tags := receiptTestTLVs(t, payload)
	for _, forbidden := range []uint16{1173, 1174, 1177} {
		if _, ok := tags[forbidden]; ok {
			t.Fatalf("legacy correction tag %d appeared in FFD 1.2 payload", forbidden)
		}
	}
	for _, raw := range []string{
		`{"prescriptionNumber":"AB"}`, `{"correctedCalculationDate":-1}`, `{"correctedCalculationDate":1.5}`,
		`{"correctedCalculationDate":4294967296}`, `{"basis":{"date":1704067200}}`, `{"correctionType":0}`,
	} {
		if err := ffd12ExtraTestCommand(t, "setReceiptAdditionalData").Validate(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid correction data %s", raw)
		}
	}
}

func TestFFD12ExtraReceiptCP866AndPadding(t *testing.T) {
	payload, _, err := ffd12ExtraTestPayload(t, "setReceiptAdditionalData", `{"cashierName":"Тест","customerINN":"1234567890","customerCitizenship":"1","customerBirthDate":"29.02.2024","customerDocumentType":21}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	tags := receiptTestTLVs(t, payload)
	if !bytes.Equal(tags[1021][0], []byte{0x92, 0xA5, 0xE1, 0xE2}) || string(tags[1228][0]) != "1234567890  " || string(tags[1244][0]) != "1  " {
		t.Fatalf("CP866 or padding mismatch: % X", payload)
	}
	if string(tags[1243][0]) != "29.02.2024" || !bytes.Equal(tags[1245][0], []byte{21}) {
		t.Fatalf("customer date or document type mismatch: % X", payload)
	}
}

func TestFFD12ExtraIndustryAttributesAreRepeatedSTLVs(t *testing.T) {
	payload, _, err := ffd12ExtraTestPayload(t, "setReceiptAdditionalData", `{"industryAttributes":[{"authorityCode":48,"documentDate":"01.01.2024","documentNumber":"N1","value":"V1"},{"authorityCode":1,"documentDate":"02.01.2024","documentNumber":"N2","value":"V2"}]}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	tags := receiptTestTLVs(t, payload)
	if len(tags[1261]) != 2 {
		t.Fatalf("expected two repeated industry STLVs: % X", payload)
	}
	for i, nested := range tags[1261] {
		children := receiptTestTLVs(t, nested)
		for _, tag := range []uint16{1262, 1263, 1264, 1265} {
			if len(children[tag]) != 1 {
				t.Fatalf("industry attribute %d lacks required tag %d", i, tag)
			}
		}
	}
}

func TestFFD12ExtraReceiptValidationAndOmission(t *testing.T) {
	for _, raw := range []string{`{}`, `{"internetPayment":0}`, `{"internetPayment":1,"settlementPlace":"example.org"}`, `{"correctedCalculationDate":0}`} {
		if err := ffd12ExtraTestCommand(t, "setReceiptAdditionalData").Validate(json.RawMessage(raw)); err != nil {
			t.Fatalf("rejected valid additional data %s: %v", raw, err)
		}
	}
	for _, raw := range []string{
		`{"internetPayment":1}`, `{"internetPayment":1,"settlementPlace":""}`, `{"internetPayment":2}`,
		`{"industryAttributes":[]}`, `{"industryAttributes":[{"authorityCode":48}]}`,
		`{"industryAttributes":[{"authorityCode":48,"documentDate":"31.02.2024","documentNumber":"N","value":"V"}]}`,
		`{"customerBirthDate":"31.02.2024"}`, `{"customerBirthDate":"2024-02-29"}`, `{"customerDocumentType":23}`,
		`{"cashierName":"😀"}`, `{"unexpected":1}`,
	} {
		if err := ffd12ExtraTestCommand(t, "setReceiptAdditionalData").Validate(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid additional data %s", raw)
		}
	}
	for _, name := range []string{"automaticDeviceNumber", "cashierINN", "prescriptionNumber"} {
		length := map[string]int{"automaticDeviceNumber": 21, "cashierINN": 13, "prescriptionNumber": 33}[name]
		values := map[string]any{name: strings.Repeat("1", length)}
		if name == "prescriptionNumber" {
			values["correctedCalculationDate"] = 1704067200
		}
		data, _ := json.Marshal(values)
		if err := ffd12ExtraTestCommand(t, "setReceiptAdditionalData").Validate(data); err == nil {
			t.Fatalf("accepted bounds violation %s", data)
		}
	}
	payload, _, err := ffd12ExtraTestPayload(t, "setReceiptAdditionalData", `{}`, nil)
	if err != nil || len(payload) != 0 {
		t.Fatalf("omitted optional fields should produce no TLVs: % X, %v", payload, err)
	}
}

func TestFFD12ExtraReportWireUnchangedAndSourceUpdated(t *testing.T) {
	for _, name := range []string{"beginSettlementReport", "finalizeSettlementReport"} {
		command := ffd12ExtraTestCommand(t, name)
		if command.Definition.Source.Version != "FFD 1.2" || command.Definition.Source.Page != 12 {
			t.Fatalf("incorrect supplement provenance: %+v", command.Definition.Source)
		}
		if err := command.Validate(json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	complete := ffd12ExtraTestCommand(t, "finalizeSettlementReport")
	device := testDevice("report", func(_ context.Context, code byte, payload []byte) ([]byte, error) {
		if code != 0x28 || len(payload) != 0 {
			t.Fatalf("unexpected report request code %02X, payload % X", code, payload)
		}
		return []byte{1, 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, nil
	}, 1)
	if _, err := complete.Execute(context.Background(), device, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("unchanged 15-byte report response rejected: %v", err)
	}
}

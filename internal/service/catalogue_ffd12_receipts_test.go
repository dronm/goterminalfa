package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
)

func ffd12ReceiptTestCommand(t *testing.T, name string) Command {
	t.Helper()
	for _, command := range ffd12ReceiptCommands() {
		if command.Definition.Name == name {
			return command
		}
	}
	t.Fatalf("FFD 1.2 receipt command %q not found", name)
	return Command{}
}

func ffd12ReceiptTestExchange(t *testing.T, name, params string, response []byte) ([]byte, any, error) {
	t.Helper()
	command := ffd12ReceiptTestCommand(t, name)
	var payload []byte
	device := testDevice("ffd12", func(_ context.Context, code byte, data []byte) ([]byte, error) {
		if fmt.Sprintf("0x%02X", code) != command.Definition.ProtocolCode {
			t.Fatalf("unexpected command code %02X", code)
		}
		payload = append([]byte{}, data...)
		return response, nil
	}, 1)
	if err := command.Validate(json.RawMessage(params)); err != nil {
		return nil, nil, err
	}
	value, err := command.Execute(context.Background(), device, json.RawMessage(params))
	return payload, value, err
}

func ffd12ReceiptResponse() []byte {
	return []byte{5, 0, 12, 0, 0, 0, 3, 0, 0, 0, 26, 11, 2, 12, 34, 42, 0}
}

func ffd12ReceiptMinimumItem() string {
	return `{"name":"A","priceKopecks":12345,"quantity":"1.25","vatRate":11,"paymentMethod":4,"paymentObject":1}`
}

func TestFFD12ReceiptBeginOptionalUINT16PrintingParameter(t *testing.T) {
	for _, name := range []string{"beginReceipt", "beginCorrectionReceipt"} {
		t.Run(name, func(t *testing.T) {
			payload, _, err := ffd12ReceiptTestExchange(t, name, `{}`, nil)
			if err != nil || len(payload) != 0 {
				t.Fatalf("omitted printing parameter: payload % X, error %v", payload, err)
			}
			payload, _, err = ffd12ReceiptTestExchange(t, name, `{"printSuppressionCode":41029}`, nil)
			if err != nil || !bytes.Equal(payload, []byte{0x45, 0xA0}) {
				t.Fatalf("printing sentinel must be UINT16 LE: payload % X, error %v", payload, err)
			}
			for _, raw := range []string{`{"printSuppressionCode":1}`, `{"printSuppressionCode":65536}`, `{"withoutPrinting":1}`, `{"correctionType":0}`} {
				if err := ffd12ReceiptTestCommand(t, name).Validate(json.RawMessage(raw)); err == nil {
					t.Fatalf("accepted undocumented begin parameter %s", raw)
				}
			}
		})
	}
}

func TestFFD12ReceiptFinalizationUsesUINT40AndCompleteResponse(t *testing.T) {
	for _, name := range []string{"finalizeReceipt", "finalizeCorrectionReceipt"} {
		t.Run(name, func(t *testing.T) {
			payload, value, err := ffd12ReceiptTestExchange(t, name, `{"calculationSign":1,"totalKopecks":1000}`, ffd12ReceiptResponse())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(payload, []byte{1, 0xE8, 3, 0, 0, 0}) {
				t.Fatalf("wrong fixed-width finalization payload: % X", payload)
			}
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if string(fields["receiptNumber"]) != "5" || string(fields["documentNumber"]) != "12" || string(fields["fiscalSign"]) != "3" || string(fields["dateTime"]) != `"2026-11-02T12:34"` || string(fields["shiftNumber"]) != "42" {
				t.Fatalf("wrong finalization output: %s", data)
			}
			for _, length := range []int{0, 10, 15, 16, 18} {
				if _, _, err := ffd12ReceiptTestExchange(t, name, `{"calculationSign":1,"totalKopecks":1000}`, make([]byte, length)); err == nil {
					t.Fatalf("accepted unsupported FFD 1.2 response length %d", length)
				}
			}
			for _, raw := range []string{`{"calculationSign":1,"totalKopecks":1099511627776}`, `{"calculationSign":1,"totalKopecks":1000,"additionalText":"A"}`, `{"calculationSign":1,"totalKopecks":1000,"legacyVatRate":6}`} {
				if err := ffd12ReceiptTestCommand(t, name).Validate(json.RawMessage(raw)); err == nil {
					t.Fatalf("accepted invalid FFD 1.2 finalization input %s", raw)
				}
			}
		})
	}
}

func TestFFD12ReceiptUnmarkedItemIsFlatTLVAndPreservesOmission(t *testing.T) {
	payload, _, err := ffd12ReceiptTestExchange(t, "addReceiptItemUnmarked", ffd12ReceiptMinimumItem(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := hex.DecodeString("0604010041370402003930ff030200027daf0401000bbe04010004bc04010001")
	if !bytes.Equal(payload, want) {
		t.Fatalf("FFD 1.2 unmarked item must be flat TLVs\n got % X\nwant % X", payload, want)
	}
	fields := receiptTestTLVs(t, payload)
	if _, exists := fields[1059]; exists {
		t.Fatal("invented STLV 1059 wrapper for command 0x96")
	}
	if _, exists := fields[2108]; exists {
		t.Fatal("injected omitted measure instead of leaving its documented default to the KKT")
	}
	var params map[string]any
	_ = json.Unmarshal([]byte(ffd12ReceiptMinimumItem()), &params)
	params["measure"], params["countryCode"] = 0, "36"
	raw, _ := json.Marshal(params)
	payload, _, err = ffd12ReceiptTestExchange(t, "addReceiptItemUnmarked", string(raw), nil)
	if err != nil {
		t.Fatal(err)
	}
	fields = receiptTestTLVs(t, payload)
	if !bytes.Equal(fields[2108][0], []byte{0}) || string(fields[1230][0]) != "36 " {
		t.Fatalf("explicit zero measure or documented country padding was lost: % X", payload)
	}
}

func TestFFD12ReceiptUnmarkedItemSchemaAndVATAppendix5(t *testing.T) {
	command := ffd12ReceiptTestCommand(t, "addReceiptItemUnmarked")
	var params map[string]any
	_ = json.Unmarshal([]byte(ffd12ReceiptMinimumItem()), &params)
	for vat := 1; vat <= 12; vat++ {
		params["vatRate"] = vat
		raw, _ := json.Marshal(params)
		if err := command.Validate(raw); err != nil {
			t.Fatalf("documented VAT value %d was rejected: %v", vat, err)
		}
	}
	params["vatRate"] = 11
	for _, object := range []int{1, 15, 16, 27, 30, 31, 32, 33} {
		params["paymentObject"] = object
		raw, _ := json.Marshal(params)
		if err := command.Validate(raw); err != nil {
			t.Fatalf("appendix 1 value %d was rejected: %v", object, err)
		}
	}
	for _, change := range []func(map[string]any){
		func(p map[string]any) { delete(p, "paymentObject") },
		func(p map[string]any) { p["vatRate"] = 13 },
		func(p map[string]any) { p["paymentObject"] = 28 },
		func(p map[string]any) { p["measure"] = 1 },
		func(p map[string]any) { p["quantity"] = 1.25 },
		func(p map[string]any) { p["nomenclatureCode"] = "05AB" },
		func(p map[string]any) { p["countryCode"] = "1234" },
		func(p map[string]any) { p["item"] = map[string]any{} },
	} {
		var invalid map[string]any
		_ = json.Unmarshal([]byte(ffd12ReceiptMinimumItem()), &invalid)
		change(invalid)
		raw, _ := json.Marshal(invalid)
		if err := command.Validate(raw); err == nil {
			t.Fatalf("accepted invalid unmarked item: %s", raw)
		}
	}
}

func TestFFD12ReceiptIndustrySTLVRepetitionAndCalendarValidation(t *testing.T) {
	var params map[string]any
	_ = json.Unmarshal([]byte(ffd12ReceiptMinimumItem()), &params)
	params["industryFields"] = []any{
		map[string]any{"authority": 48, "documentDate": "02.11.2026", "documentNumber": "N1", "value": "A"},
		map[string]any{"authority": 34, "documentDate": "29.02.2024", "documentNumber": "N2", "value": "B"},
	}
	raw, _ := json.Marshal(params)
	payload, _, err := ffd12ReceiptTestExchange(t, "addReceiptItemUnmarked", string(raw), nil)
	if err != nil {
		t.Fatal(err)
	}
	fields := receiptTestTLVs(t, payload)
	if len(fields[1260]) != 2 {
		t.Fatalf("industry STLV count: %d", len(fields[1260]))
	}
	first := receiptTestTLVs(t, fields[1260][0])
	second := receiptTestTLVs(t, fields[1260][1])
	if !bytes.Equal(first[1262][0], []byte{48}) || string(first[1263][0]) != "02.11.2026" || string(first[1264][0]) != "N1" || string(first[1265][0]) != "A" || string(second[1264][0]) != "N2" {
		t.Fatal("nested industry fields lost values or ordering")
	}
	for _, entry := range []map[string]any{
		{"authority": 48, "documentDate": "31.02.2026", "documentNumber": "N", "value": "A"},
		{"authority": 48, "documentDate": "2026-11-02", "documentNumber": "N", "value": "A"},
		{"authority": 48, "documentDate": "02.11.2026", "documentNumber": "N"},
		{"authority": 73, "documentDate": "02.11.2026", "documentNumber": "N", "value": "A"},
	} {
		params["industryFields"] = []any{entry}
		raw, _ := json.Marshal(params)
		if err := ffd12ReceiptTestCommand(t, "addReceiptItemUnmarked").Validate(raw); err == nil {
			t.Fatalf("accepted invalid industry entry %s", raw)
		}
	}
}

func TestFFD12ReceiptAgentRequirementsAndSupplierINNPadding(t *testing.T) {
	var params map[string]any
	_ = json.Unmarshal([]byte(ffd12ReceiptMinimumItem()), &params)
	for _, agent := range []int{1, 2, 4, 8, 16, 32, 64} {
		params["agentSign"] = agent
		delete(params, "supplierINN")
		raw, _ := json.Marshal(params)
		if err := ffd12ReceiptTestCommand(t, "addReceiptItemUnmarked").Validate(raw); err == nil {
			t.Fatalf("agent role %d must supply the explicitly mandatory supplier INN", agent)
		}
		params["supplierINN"] = "1234567890"
		raw, _ = json.Marshal(params)
		payload, _, err := ffd12ReceiptTestExchange(t, "addReceiptItemUnmarked", string(raw), nil)
		if err != nil {
			t.Fatalf("starred details must remain optional for agent role %d: %v", agent, err)
		}
		fields := receiptTestTLVs(t, payload)
		if string(fields[1226][0]) != "1234567890  " {
			t.Fatalf("wrong supplier INN padding for agent role %d: % X", agent, fields[1226][0])
		}
		for _, tag := range []uint16{1075, 1044, 1073, 1074, 1026, 1005, 1016, 1171, 1225} {
			if _, supplied := fields[tag]; supplied {
				t.Fatalf("driver injected omitted optional agent tag %d", tag)
			}
		}
	}
	params["agentSign"], params["supplierName"], params["supplierPhones"] = 64, "S", []any{"123", "456"}
	raw, _ := json.Marshal(params)
	payload, _, err := ffd12ReceiptTestExchange(t, "addReceiptItemUnmarked", string(raw), nil)
	if err != nil {
		t.Fatal(err)
	}
	fields := receiptTestTLVs(t, payload)
	if string(fields[1226][0]) != "1234567890  " || len(fields[1171]) != 2 || string(fields[1171][1]) != "456" {
		t.Fatal("supplier INN padding or repeated phone encoding is wrong")
	}
	params["agentSign"] = 4
	params["paymentAgentPhones"], params["paymentAcceptancePhones"] = []any{"789"}, []any{"012"}
	raw, _ = json.Marshal(params)
	if err := ffd12ReceiptTestCommand(t, "addReceiptItemUnmarked").Validate(raw); err != nil {
		t.Fatal(err)
	}
	params["agentSign"] = 0
	raw, _ = json.Marshal(params)
	if err := ffd12ReceiptTestCommand(t, "addReceiptItemUnmarked").Validate(raw); err != nil {
		t.Fatalf("FFD 1.2 supplement does not prohibit optional details for agentSign 0: %v", err)
	}
}

func TestFFD12ReceiptOptionalAgentMetadataDoesNotClaimConditionalRequirements(t *testing.T) {
	for _, field := range ffd12UnmarkedItemFields() {
		switch field.Name {
		case "transferOperatorPhones", "transferOperation", "paymentAgentPhones", "paymentAcceptancePhones", "transferOperatorName", "transferOperatorAddress", "transferOperatorINNs", "supplierPhones", "supplierName":
			if field.Required || field.RequiredIf != "" {
				t.Fatalf("starred field %s incorrectly claims a required condition", field.Name)
			}
			if field.Notes == "" {
				t.Fatalf("optional field %s should still describe its documented applicability", field.Name)
			}
		}
	}
}

func TestFFD12ReceiptPaymentLayoutAndBaseCompatibility(t *testing.T) {
	params := `{"taxSystem":1,"cashKopecks":100,"cashlessKopecks":0,"prepaymentKopecks":0,"creditKopecks":0,"counterProvisionKopecks":0}`
	payload, _, err := ffd12ReceiptTestExchange(t, "setReceiptPayment", params, nil)
	if err != nil {
		t.Fatal(err)
	}
	fields := receiptTestTLVs(t, payload)
	if len(fields) != 6 || !bytes.Equal(fields[1055][0], []byte{1}) || !bytes.Equal(fields[1031][0], []byte{100}) {
		t.Fatalf("unexpected FFD 1.2 payment tags: % X", payload)
	}
	for _, extra := range []string{`,"taxSystemLegacy":8`, `,"customerContact":"a@example.org"`, `,"cashierName":"A"`, `,"internetPayment":1`} {
		invalid := params[:len(params)-1] + extra + "}"
		if err := ffd12ReceiptTestCommand(t, "setReceiptPayment").Validate(json.RawMessage(invalid)); err == nil {
			t.Fatalf("FFD 1.2 payment command accepted extra data %s", invalid)
		}
	}
	if err := ffd12ReceiptTestCommand(t, "setReceiptPayment").Validate(json.RawMessage(`{"taxSystem":8,"cashKopecks":100,"cashlessKopecks":0,"prepaymentKopecks":0,"creditKopecks":0,"counterProvisionKopecks":0}`)); err == nil {
		t.Fatal("FFD 1.2 taxation dictionary does not include ENVD value 8")
	}
	// The separate FFD definitions must not mutate the previously supported base
	// command shapes or metadata.
	if err := receiptTestCommand(t, "finalizeCorrectionReceipt").Validate(json.RawMessage(`{"calculationSign":1,"totalKopecks":1000,"legacyVatRate":6}`)); err != nil {
		t.Fatalf("base legacy compatibility was lost: %v", err)
	}
	if err := receiptTestCommand(t, "finalizeReceipt").Validate(json.RawMessage(`{"calculationSign":1,"totalKopecks":1000,"additionalText":"A"}`)); err != nil {
		t.Fatalf("base additional text was lost: %v", err)
	}
	if err := receiptTestCommand(t, "beginReceipt").Validate(json.RawMessage(`{"printSuppressionCode":41029}`)); err == nil {
		t.Fatal("FFD printing parameter leaked into the base profile")
	}
}

func TestFFD12ReceiptMetadataUsesSuppliedSupplement(t *testing.T) {
	for _, command := range ffd12ReceiptCommands() {
		if command.Definition.Source.Document != ffd12ReceiptDocument || command.Definition.Source.Version != "FFD 1.2" || command.Definition.Source.Page < 22 || command.Definition.Source.Page > 28 {
			t.Fatalf("wrong FFD source attribution: %+v", command.Definition.Source)
		}
		if command.Definition.ReadOnly || !command.Definition.IdempotencyRequired || !command.Definition.OperationRequired {
			t.Fatalf("FFD receipt command lost operation/idempotency policy: %+v", command.Definition)
		}
	}
}

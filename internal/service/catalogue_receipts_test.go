package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
)

func receiptTestCommand(t *testing.T, name string) Command {
	t.Helper()
	for _, cmd := range receiptCommands() {
		if cmd.Definition.Name == name {
			return cmd
		}
	}
	t.Fatalf("receipt command %q not found", name)
	return Command{}
}

func receiptTestExchange(t *testing.T, name, params string, response []byte) ([]byte, any, error) {
	t.Helper()
	cmd := receiptTestCommand(t, name)
	var payload []byte
	device := testDevice("receipts", func(_ context.Context, code byte, data []byte) ([]byte, error) {
		if fmt.Sprintf("0x%02X", code) != cmd.Definition.ProtocolCode {
			t.Fatalf("wrong code: %02X for %s", code, cmd.Definition.ProtocolCode)
		}
		payload = append([]byte{}, data...)
		return response, nil
	}, 1)
	if err := cmd.Validate(json.RawMessage(params)); err != nil {
		return nil, nil, err
	}
	result, err := cmd.Execute(context.Background(), device, json.RawMessage(params))
	return payload, result, err
}

func receiptTestTLVs(t *testing.T, data []byte) map[uint16][][]byte {
	t.Helper()
	values := map[uint16][][]byte{}
	for len(data) > 0 {
		if len(data) < 4 {
			t.Fatalf("short TLV header: % X", data)
		}
		tag := binary.LittleEndian.Uint16(data)
		n := int(binary.LittleEndian.Uint16(data[2:]))
		if len(data) < 4+n {
			t.Fatalf("short TLV body: % X", data)
		}
		values[tag] = append(values[tag], append([]byte{}, data[4:4+n]...))
		data = data[4+n:]
	}
	return values
}

func TestReceiptItemGoldenNestedTLVAndUpdatedVAT(t *testing.T) {
	payload, _, err := receiptTestExchange(t, "addReceiptItem", `{"item":{"name":"A","priceKopecks":12345,"quantity":"1.25","vatRate":11,"paymentMethod":4}}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, err := hex.DecodeString("23041b000604010041370402003930ff030200027daf0401000bbe04010004")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, want) {
		t.Fatalf("wrong item payload\n got % X\nwant % X", payload, want)
	}
	for vat := 1; vat <= 12; vat++ {
		t.Run(fmt.Sprintf("VAT%d", vat), func(t *testing.T) {
			params := fmt.Sprintf(`{"item":{"name":"A","priceKopecks":1,"quantity":"1","vatRate":%d,"paymentMethod":4}}`, vat)
			if err := receiptTestCommand(t, "addReceiptItem").Validate(json.RawMessage(params)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReceiptItemPreservesExactNumbersAndASCIIHex(t *testing.T) {
	payload, _, err := receiptTestExchange(t, "addReceiptItem", `{"item":{"name":"A","priceKopecks":9007199254740993,"quantity":"1.25","vatRate":12,"paymentMethod":4,"nomenclatureCode":"05AB"}}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	outer := receiptTestTLVs(t, payload)
	inner := receiptTestTLVs(t, outer[1059][0])
	if !bytes.Equal(inner[1079][0], []byte{1, 0, 0, 0, 0, 0, 32}) {
		t.Fatalf("price lost precision: % X", inner[1079][0])
	}
	if string(inner[1162][0]) != "05AB" {
		t.Fatalf("nomenclatureCode must contain ASCII HEX, got % X", inner[1162][0])
	}
}

func TestReceiptItemValidation(t *testing.T) {
	for _, params := range []string{
		`{"item":{"name":"A","priceKopecks":1,"quantity":1.25,"vatRate":1,"paymentMethod":4}}`,
		`{"item":{"name":"A","priceKopecks":1.5,"quantity":"1","vatRate":1,"paymentMethod":4}}`,
		`{"item":{"name":"A","priceKopecks":1,"quantity":"1","vatRate":13,"paymentMethod":4}}`,
		`{"item":{"name":"A","priceKopecks":1,"quantity":"1","vatRate":1,"paymentMethod":4,"nomenclatureCode":"ABC"}}`,
		`{"item":{"name":"A","priceKopecks":1,"quantity":"1","vatRate":1,"paymentMethod":4,"extra":1}}`,
		`{"item":{"name":"ordinary name","priceKopecks":1,"quantity":"1","vatRate":1,"paymentMethod":4,"paymentObject":15}}`,
		`{"item":{"name":"32","priceKopecks":1,"quantity":"1","vatRate":1,"paymentMethod":4,"paymentObject":16}}`,
		`{"item":{"name":"A","priceKopecks":1,"quantity":"1","vatRate":1,"paymentMethod":4,"supplierName":"S"}}`,
	} {
		if err := receiptTestCommand(t, "addReceiptItem").Validate(json.RawMessage(params)); err == nil {
			t.Fatalf("accepted invalid item: %s", params)
		}
	}
}

func TestReceiptItemAppendix7DoesNotInventPaymentObjectPartitions(t *testing.T) {
	for _, params := range []string{
		`{"item":{"name":"26","priceKopecks":1,"quantity":"1","vatRate":1,"paymentMethod":4,"paymentObject":15}}`,
		`{"item":{"name":"1","priceKopecks":1,"quantity":"1","vatRate":1,"paymentMethod":4,"paymentObject":16}}`,
	} {
		if err := receiptTestCommand(t, "addReceiptItem").Validate(json.RawMessage(params)); err != nil {
			t.Fatalf("manual does not prescribe separate name-code ranges for payment objects 15 and 16: %v", err)
		}
	}
}

func TestReceiptFinalizeUINT40Boundary(t *testing.T) {
	response := []byte{1, 0, 2, 0, 0, 0, 3, 0, 0, 0}
	payload, _, err := receiptTestExchange(t, "finalizeReceipt", `{"calculationSign":1,"totalKopecks":1099511627775}`, response)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, []byte{1, 255, 255, 255, 255, 255}) {
		t.Fatalf("wrong UINT40 encoding: % X", payload)
	}
	for _, value := range []string{"1099511627776", "1.1", "-1"} {
		params := json.RawMessage(`{"calculationSign":1,"totalKopecks":` + value + `}`)
		if err := receiptTestCommand(t, "finalizeReceipt").Validate(params); err == nil {
			t.Fatalf("accepted invalid UINT40 %s", value)
		}
	}
}

func TestReceiptFinalizeDocumentedResponseVersions(t *testing.T) {
	base := []byte{5, 0, 12, 0, 0, 0, 3, 0, 0, 0}
	response := append(append([]byte{}, base...), 26, 10, 30, 12, 34, 42, 0)
	for _, length := range []int{10, 15, 17} {
		t.Run(fmt.Sprintf("%dbytes", length), func(t *testing.T) {
			_, value, err := receiptTestExchange(t, "finalizeReceipt", `{"calculationSign":1,"totalKopecks":10}`, response[:length])
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if string(fields["receiptNumber"]) != "5" || string(fields["documentNumber"]) != "12" || string(fields["fiscalSign"]) != "3" {
				t.Fatalf("wrong receipt identifiers: %s", data)
			}
			if length >= 15 && string(fields["dateTime"]) != `"2026-10-30T12:34"` {
				t.Fatalf("wrong date/time: %s", data)
			}
			if length == 17 && string(fields["shiftNumber"]) != "42" {
				t.Fatalf("wrong shift: %s", data)
			}
		})
	}
	for _, length := range []int{0, 9, 11, 14, 16, 18} {
		if _, _, err := receiptTestExchange(t, "finalizeReceipt", `{"calculationSign":1,"totalKopecks":10}`, make([]byte, length)); err == nil {
			t.Fatalf("accepted malformed response length %d", length)
		}
	}
}

func TestCorrectionFinalizeModernAndExplicitLegacyLayout(t *testing.T) {
	for _, tc := range []struct {
		params string
		want   []byte
	}{
		{`{"calculationSign":1,"totalKopecks":1000}`, []byte{1, 0xE8, 3}},
		{`{"calculationSign":1,"legacyVatRate":6,"totalKopecks":1000}`, []byte{1, 6, 0xE8, 3}},
	} {
		payload, _, err := receiptTestExchange(t, "finalizeCorrectionReceipt", tc.params, make([]byte, 10))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(payload, tc.want) {
			t.Fatalf("wrong correction payload: got % X, want % X", payload, tc.want)
		}
	}
}

func receiptCorrectionExample() string {
	return `{"authorizedPersonName":"A","authorizedPersonINN":"123456789012","correctionType":0,"taxSystem":1,"cashKopecks":100,"cashlessKopecks":0,"prepaymentKopecks":0,"creditKopecks":0,"counterProvisionKopecks":0,"vat22BaseKopecks":100,"basis":{"name":"Act","date":1767225600,"number":"1"}}`
}

func TestCorrectionAppendix10TagsAndNestedBasis(t *testing.T) {
	payload, _, err := receiptTestExchange(t, "setCorrectionData", receiptCorrectionExample(), nil)
	if err != nil {
		t.Fatal(err)
	}
	fields := receiptTestTLVs(t, payload)
	if !bytes.Equal(fields[40011][0], []byte{100}) {
		t.Fatalf("missing 22%% VAT base tag 40011: % X", payload)
	}
	if _, present := fields[1104]; present {
		t.Fatal("driver invented omitted zero-rate tag instead of allowing KKT appendix-10 behavior")
	}
	basis := receiptTestTLVs(t, fields[1174][0])
	if string(basis[1177][0]) != "Act" || string(basis[1023][0]) != "1" || len(basis[1178][0]) != 4 {
		t.Fatalf("wrong nested correction basis: %v", basis)
	}
	if _, present := basis[1179]; present {
		t.Fatal("driver silently substituted undocumented tag 1179 for documented tag 1023")
	}
}

func TestCorrectionTotalsAndInternetDependencies(t *testing.T) {
	var params map[string]any
	if err := json.Unmarshal([]byte(receiptCorrectionExample()), &params); err != nil {
		t.Fatal(err)
	}
	cmd := receiptTestCommand(t, "setCorrectionData")
	params["vat22BaseKopecks"] = 99
	raw, _ := json.Marshal(params)
	if err := cmd.Validate(raw); err == nil {
		t.Fatal("accepted mismatched correction totals")
	}
	params["vat22BaseKopecks"] = 100
	params["internetPayment"] = 1
	raw, _ = json.Marshal(params)
	if err := cmd.Validate(raw); err == nil {
		t.Fatal("accepted internet payment without customer contact/site")
	}
	params["settlementPlace"], params["customerContact"] = "https://example.org", "buyer@example.org"
	raw, _ = json.Marshal(params)
	if err := cmd.Validate(raw); err != nil {
		t.Fatal(err)
	}
}

func TestReceiptInternetPaymentZeroIsExplicitAndDoesNotRequireInternetFields(t *testing.T) {
	params := `{"taxSystem":1,"cashKopecks":100,"cashlessKopecks":0,"prepaymentKopecks":0,"creditKopecks":0,"counterProvisionKopecks":0,"internetPayment":0}`
	payload, _, err := receiptTestExchange(t, "setReceiptPayment", params, nil)
	if err != nil {
		t.Fatal(err)
	}
	fields := receiptTestTLVs(t, payload)
	if !bytes.Equal(fields[1125][0], []byte{0}) {
		t.Fatalf("explicit internetPayment 0 was not preserved: % X", payload)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(params), &object); err != nil {
		t.Fatal(err)
	}
	delete(object, "internetPayment")
	omitted, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	payload, _, err = receiptTestExchange(t, "setReceiptPayment", string(omitted), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := receiptTestTLVs(t, payload)[1125]; exists {
		t.Fatal("omitted internetPayment became an explicit zero tag")
	}
	var correction map[string]any
	if err := json.Unmarshal([]byte(receiptCorrectionExample()), &correction); err != nil {
		t.Fatal(err)
	}
	correction["internetPayment"] = 0
	raw, _ := json.Marshal(correction)
	if err := receiptTestCommand(t, "setCorrectionData").Validate(raw); err != nil {
		t.Fatalf("correction internetPayment 0 unexpectedly requires internet-only fields: %v", err)
	}
}

func TestSharedAgentZeroAndConditionalFields(t *testing.T) {
	if _, _, err := receiptTestExchange(t, "setReceiptAgentData", `{"agentSign":0}`, nil); err != nil {
		t.Fatal(err)
	}
	for _, params := range []string{
		`{"agentSign":0,"supplierPhone":"123"}`,
		`{"agentSign":4,"supplierPhone":"123"}`,
		`{"agentSign":3,"supplierPhone":"123"}`,
	} {
		if err := receiptTestCommand(t, "setReceiptAgentData").Validate(json.RawMessage(params)); err == nil {
			t.Fatalf("accepted invalid agent parameters %s", params)
		}
	}
	if _, _, err := receiptTestExchange(t, "setReceiptAgentData", `{"agentSign":4,"supplierPhone":"123","paymentAgentPhone":"456","paymentAcceptancePhone":"789"}`, nil); err != nil {
		t.Fatal(err)
	}
	// The operator INN column specifies a maximum, not an exact length.
	if _, _, err := receiptTestExchange(t, "setReceiptAgentData", `{"agentSign":1,"supplierPhone":"123","transferOperation":"op","transferOperatorName":"N","transferOperatorINN":"1234567890","transferOperatorAddress":"A","transferOperatorPhone":"456"}`, nil); err != nil {
		t.Fatal(err)
	}
}

func TestReceiptItemRepeatedAgentTagsAndSharedDataOption(t *testing.T) {
	payload, _, err := receiptTestExchange(t, "addReceiptItem", `{"item":{"name":"A","priceKopecks":1,"quantity":"1","vatRate":1,"paymentMethod":4,"agentSign":4,"supplierPhones":["123","456"],"paymentAgentPhones":["789"],"paymentAcceptancePhones":["012"]}}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	fields := receiptTestTLVs(t, receiptTestTLVs(t, payload)[1059][0])
	if len(fields[1171]) != 2 || string(fields[1171][0]) != "123" || string(fields[1171][1]) != "456" {
		t.Fatalf("repeated phone tags lost their order: %v", fields[1171])
	}
	cmd := receiptTestCommand(t, "addReceiptItem")
	if err := cmd.Validate(json.RawMessage(`{"item":{"name":"A","priceKopecks":1,"quantity":"1","vatRate":1,"paymentMethod":4,"agentSign":4}}`)); err != nil {
		t.Fatalf("shared agent data may be supplied later with 0x2C: %v", err)
	}
	if err := cmd.Validate(json.RawMessage(`{"item":{"name":"A","priceKopecks":1,"quantity":"1","vatRate":1,"paymentMethod":4,"agentSign":4,"supplierPhones":["123"]}}`)); err == nil {
		t.Fatal("accepted incomplete per-item agent data")
	}
}

func TestReceiptDeviceDetailsPresenceAndRequestPolicy(t *testing.T) {
	if err := receiptTestCommand(t, "setReceiptDeviceDetails").Validate(json.RawMessage(`{"settlementAddress":"A","settlementPlace":"P"}`)); err == nil {
		t.Fatal("normal receipt requires all three device-detail tags")
	}
	if err := receiptTestCommand(t, "setCorrectionDeviceDetails").Validate(json.RawMessage(`{"settlementAddress":"A","settlementPlace":"P"}`)); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range receiptCommands() {
		if cmd.Definition.ReadOnly || !cmd.Definition.IdempotencyRequired {
			t.Fatalf("receipt operation must be journaled: %s", cmd.Definition.Name)
		}
	}
}

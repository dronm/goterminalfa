package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/dronm/goterminalfa/internal/protocol"
	"github.com/dronm/goterminalfa/internal/service"
)

const ffd12HTTPCorrectionParams = `{"cashierName":"A","suppressShiftPrint":1,"printSuppressionCode":41029,"items":[` + ffd12HTTPItem + `],"additionalData":{"correctedCalculationDate":1704067200},"payment":` + ffd12HTTPPayment + `,"calculationSign":1,"totalKopecks":100}`

func ffd12HTTPCorrectionResponse(code, shiftState byte) []byte {
	switch code {
	case 0x02:
		return []byte("550101046182")
	case 0x20:
		return []byte{shiftState, 4, 0, 2, 0}
	case 0x22:
		return []byte{5, 0, 6, 0, 0, 0, 7, 0, 0, 0, 0}
	case 0x26:
		return []byte{1, 0, 7, 0, 0, 0, 9, 0, 0, 0, 26, 11, 17, 10, 30, 5, 0}
	default:
		return nil
	}
}

func TestCreateCorrectionReceiptHTTPShiftBranchesAndReplay(t *testing.T) {
	for _, test := range []struct {
		name       string
		shiftState byte
		want       []byte
	}{
		{"open shift", 1, []byte{0x02, 0x20, 0x25, 0x96, 0x98, 0x2D, 0x26}},
		{"closed shift", 0, []byte{0x02, 0x20, 0x21, 0x2F, 0x22, 0x25, 0x96, 0x98, 0x2D, 0x26}},
	} {
		t.Run(test.name, func(t *testing.T) {
			codes := []byte{}
			api := mixedFFD12API(t, nil, func(_ context.Context, code byte, payload []byte) ([]byte, error) {
				codes = append(codes, code)
				switch code {
				case 0x21:
					if !bytes.Equal(payload, []byte{1}) {
						t.Errorf("incorrect shift printing byte: % X", payload)
					}
				case 0x25:
					if !bytes.Equal(payload, []byte{0x45, 0xA0}) {
						t.Errorf("correction printing parameter must be UINT16 LE: % X", payload)
					}
				case 0x98:
					want := []byte{0x9A, 0x04, 4, 0, 0x80, 0x00, 0x92, 0x65}
					if !bytes.Equal(payload, want) {
						t.Errorf("correction date must be direct UNIXTIME TLV1178: got % X want % X", payload, want)
					}
				case 0x26:
					if !bytes.Equal(payload, []byte{1, 100, 0, 0, 0, 0}) {
						t.Errorf("correction total must be fixed UINT40 LE: % X", payload)
					}
				}
				return ffd12HTTPCorrectionResponse(code, test.shiftState), nil
			})
			first := ffd12Post(t, api, "ffd-kkt", "createCorrectionReceipt", "correction-auto-shift", ffd12HTTPCorrectionParams)
			var response struct {
				Command string `json:"cmd"`
				Status  string `json:"status"`
				Data    struct {
					ShiftOpenedByRequest bool                       `json:"shiftOpenedByRequest"`
					ShiftResult          map[string]json.RawMessage `json:"shiftResult"`
					Receipt              map[string]json.RawMessage `json:"receipt"`
				} `json:"data"`
			}
			if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if first.Code != http.StatusOK || response.Command != "createCorrectionReceipt" || response.Status != "completed" || response.Data.ShiftOpenedByRequest != (test.shiftState == 0) || !bytes.Equal(codes, test.want) {
				t.Fatalf("incorrect correction workflow: HTTP %d %s codes=% X", first.Code, first.Body, codes)
			}
			if len(response.Data.Receipt) != 5 || string(response.Data.Receipt["receiptNumber"]) != "1" || string(response.Data.Receipt["documentNumber"]) != "7" || string(response.Data.Receipt["fiscalSign"]) != "9" || string(response.Data.Receipt["dateTime"]) != `"2026-11-17T10:30"` || string(response.Data.Receipt["shiftNumber"]) != "5" {
				t.Fatalf("correction fiscal result was not fully decoded: %s", first.Body)
			}
			if test.shiftState == 0 && (len(response.Data.ShiftResult) != 4 || string(response.Data.ShiftResult["shiftNumber"]) != "5" || string(response.Data.ShiftResult["warningFlags"]) != "0") || test.shiftState == 1 && response.Data.ShiftResult != nil {
				t.Fatalf("incorrect conditional shift result: %s", first.Body)
			}
			replay := ffd12Post(t, api, "ffd-kkt", "createCorrectionReceipt", "correction-auto-shift", ffd12HTTPCorrectionParams)
			if replay.Code != http.StatusOK || replay.Header().Get("Idempotency-Replayed") != "true" || !bytes.Equal(first.Body.Bytes(), replay.Body.Bytes()) || !bytes.Equal(codes, test.want) {
				t.Fatalf("correction receipt was not replayed without device traffic: HTTP %d %s codes=% X", replay.Code, replay.Body, codes)
			}
		})
	}
}

func TestCreateCorrectionReceiptHTTPRejectsInvalidRequestsBeforeHardware(t *testing.T) {
	api := mixedFFD12API(t, nil, func(context.Context, byte, []byte) ([]byte, error) {
		t.Fatal("invalid correction request contacted hardware")
		return nil, nil
	})
	for _, test := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing additional data", func(params map[string]any) { delete(params, "additionalData") }},
		{"missing corrected date", func(params map[string]any) { params["additionalData"] = map[string]any{"cashierName": "A"} }},
		{"null corrected date", func(params map[string]any) {
			params["additionalData"] = map[string]any{"correctedCalculationDate": nil}
		}},
		{"out-of-range corrected date", func(params map[string]any) {
			params["additionalData"] = map[string]any{"correctedCalculationDate": uint64(4294967296)}
		}},
		{"missing cashier", func(params map[string]any) { delete(params, "cashierName") }},
		{"empty items", func(params map[string]any) { params["items"] = []any{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			params := map[string]any{}
			if err := json.Unmarshal([]byte(ffd12HTTPCorrectionParams), &params); err != nil {
				t.Fatal(err)
			}
			test.change(params)
			body, err := json.Marshal(params)
			if err != nil {
				t.Fatal(err)
			}
			requireAPIError(t, ffd12Post(t, api, "ffd-kkt", "createCorrectionReceipt", "invalid-correction", string(body)), http.StatusBadRequest, "invalid_request")
		})
	}
	requireAPIError(t, ffd12Post(t, api, "ffd-kkt", "createCorrectionReceipt", "", ffd12HTTPCorrectionParams), http.StatusBadRequest, "invalid_request")
	requireAPIError(t, ffd12Post(t, api, "base-kkt", "createCorrectionReceipt", "base-correction", ffd12HTTPCorrectionParams), http.StatusNotFound, "unknown_command")
	nested := `[{"cmd":"createCorrectionReceipt","params":` + ffd12HTTPCorrectionParams + `}]`
	requireAPIError(t, ffd12Operation(t, api, "ffd-kkt", "nested-correction", nested), http.StatusBadRequest, "invalid_request")
}

func TestCreateCorrectionReceiptHTTPDiscoveryKeepsNormalMetadataIndependent(t *testing.T) {
	api := mixedFFD12API(t, nil, nil)
	w := request(t, api, http.MethodGet, "/api/v1/commands?deviceId=ffd-kkt", "", "", "")
	var catalogue struct {
		Commands []service.CommandDefinition `json:"commands"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &catalogue); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("correction discovery failed: HTTP %d %s", w.Code, w.Body)
	}
	byName := make(map[string]service.CommandDefinition)
	for _, command := range catalogue.Commands {
		byName[command.Name] = command
	}
	for _, name := range []string{"createReceipt", "createCorrectionReceipt"} {
		definition, present := byName[name]
		if !present || !definition.Implemented || definition.ProtocolProfile != "ffd1.2" || definition.ProtocolCode != "" || definition.ReadOnly || !definition.IdempotencyRequired || definition.OperationRequired || definition.RequestLayout != "application" || definition.ResponseLayout != "application" {
			t.Fatalf("invalid receipt operation metadata: %+v", definition)
		}
		fields := make(map[string]service.ParameterDefinition)
		for _, field := range definition.Input {
			fields[field.Name] = field
		}
		additional := fields["additionalData"]
		if additional.Required != (name == "createCorrectionReceipt") || !fields["cashierName"].Required || !fields["items"].Required || fields["items"].MinItems != 1 || fields["items"].MaxItems != 200 || fields["payment"].Required {
			t.Fatalf("receipt input requirements are incorrect for %s: %+v", name, fields)
		}
		dateFound := false
		for _, field := range additional.Children {
			if field.Name == "correctedCalculationDate" {
				dateFound = true
				if field.Required != (name == "createCorrectionReceipt") || field.WireType != "UNIXTIME" || field.Tag != 1178 {
					t.Fatalf("correction date requirement leaked between receipt operations: name=%s field=%+v", name, field)
				}
			}
		}
		if !dateFound {
			t.Fatalf("correction date field is missing from %s metadata", name)
		}
	}
}

func TestCreateCorrectionReceiptHTTPRecoversOwnedDocumentAndPreservesUnknown(t *testing.T) {
	for _, test := range []struct {
		name       string
		failCode   byte
		err        error
		status     int
		errorCode  string
		failedName string
		want       []byte
	}{
		{"item rejected", 0x96, &protocol.DeviceError{Code: 0x11}, http.StatusBadGateway, "device_error", "addReceiptItemUnmarked", []byte{0x02, 0x20, 0x25, 0x96, 0x02, 0x10}},
		{"finalization response lost", 0x26, io.ErrUnexpectedEOF, http.StatusConflict, "outcome_unknown", "finalizeCorrectionReceipt", []byte{0x02, 0x20, 0x25, 0x96, 0x98, 0x2D, 0x26, 0x02, 0x10}},
	} {
		t.Run(test.name, func(t *testing.T) {
			codes := []byte{}
			api := mixedFFD12API(t, nil, func(_ context.Context, code byte, _ []byte) ([]byte, error) {
				codes = append(codes, code)
				if code == test.failCode {
					return nil, test.err
				}
				return ffd12HTTPCorrectionResponse(code, 1), nil
			})
			first := ffd12Post(t, api, "ffd-kkt", "createCorrectionReceipt", "failed-correction", ffd12HTTPCorrectionParams)
			requireAPIError(t, first, test.status, test.errorCode)
			var response struct {
				Command string `json:"cmd"`
				Status  string `json:"status"`
				Data    struct {
					FailedStage     string `json:"failedStage"`
					FailedItemIndex *int   `json:"failedItemIndex"`
					Cleanup         struct {
						Attempted bool `json:"attempted"`
						Succeeded bool `json:"succeeded"`
					} `json:"cleanup"`
				} `json:"data"`
			}
			if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Command != "createCorrectionReceipt" || response.Data.FailedStage != test.failedName || !response.Data.Cleanup.Attempted || !response.Data.Cleanup.Succeeded || !bytes.Equal(codes, test.want) {
				t.Fatalf("incorrect owned-document recovery: HTTP %d %s codes=% X", first.Code, first.Body, codes)
			}
			if test.failCode == 0x96 && (response.Data.FailedItemIndex == nil || *response.Data.FailedItemIndex != 0) || test.failCode == 0x26 && response.Status != "unknown" {
				t.Fatalf("item index or unknown finalization diagnostic is incorrect: %s", first.Body)
			}
			replay := ffd12Post(t, api, "ffd-kkt", "createCorrectionReceipt", "failed-correction", ffd12HTTPCorrectionParams)
			lookup := request(t, api, http.MethodGet, "/api/v1/requests?deviceId=ffd-kkt&requestId=failed-correction", "", "", "")
			if replay.Code != test.status || lookup.Code != test.status || replay.Header().Get("Idempotency-Replayed") != "true" || !bytes.Equal(replay.Body.Bytes(), first.Body.Bytes()) || !bytes.Equal(lookup.Body.Bytes(), first.Body.Bytes()) || !bytes.Equal(codes, test.want) {
				t.Fatalf("failed or unknown correction was resent or lost diagnostics: replay=%s lookup=%s codes=% X", replay.Body, lookup.Body, codes)
			}
		})
	}
}

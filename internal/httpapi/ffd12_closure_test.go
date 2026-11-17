package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dronm/goterminalfa/internal/protocol"
)

const ffd12HTTPClosureData = `{"documentAddress":"A","documentPlace":"P","reportAttribute":"R","reportDataHex":"00ff"}`

func TestFFD12HTTPClosureNumericWorkflowAndNamedReplay(t *testing.T) {
	codes := []byte{}
	api := mixedFFD12API(t, nil, func(_ context.Context, code byte, payload []byte) ([]byte, error) {
		codes = append(codes, code)
		switch code {
		case 0x14, 0x28:
			if len(payload) != 0 {
				t.Errorf("closure start/finalizer must have no payload: command=%02X payload=% X", code, payload)
			}
		case 0x17:
			want := []byte{0xF1, 0x03, 1, 0, 'A', 0xA3, 0x04, 1, 0, 'P', 0x02, 0x05, 1, 0, 'R', 0x03, 0x05, 2, 0, 0, 0xFF}
			if !bytes.Equal(payload, want) {
				t.Errorf("closure report data has incorrect TLVs: got % X want % X", payload, want)
			}
		default:
			t.Fatalf("unexpected closure command %02X", code)
		}
		if code == 0x28 {
			return []byte{0x78, 0x56, 0x34, 0x12, 0xEF, 0xCD, 0xAB, 0x89}, nil
		}
		return nil, nil
	})
	numeric := `[{"cmd":"0x14"},{"cmd":"0x17","params":` + ffd12HTTPClosureData + `},{"cmd":"0x28"}]`
	first := ffd12Operation(t, api, "ffd-kkt", "fn-closure", numeric)
	results := ffd12OperationResults(t, first)
	if !bytes.Equal(codes, []byte{0x14, 0x17, 0x28}) || len(results) != 3 || results[0].Command != "beginFiscalStorageClosure" || results[1].Command != "sendFiscalStorageClosureData" || results[2].Command != "finalizeFiscalStorageClosure" {
		t.Fatalf("numeric closure was not resolved in context: codes=% X response=%s", codes, first.Body)
	}
	if len(results[2].Data) != 2 || string(results[2].Data["documentNumber"]) != "305419896" || string(results[2].Data["fiscalSign"]) != "2309737967" {
		t.Fatalf("closure did not decode its eight-byte fiscal result: %s", first.Body)
	}
	named := `[{"cmd":"beginFiscalStorageClosure"},{"cmd":"sendFiscalStorageClosureData","params":` + ffd12HTTPClosureData + `},{"cmd":"finalizeFiscalStorageClosure"}]`
	replay := ffd12Operation(t, api, "ffd-kkt", "fn-closure", named)
	if replay.Code != http.StatusOK || replay.Header().Get("Idempotency-Replayed") != "true" || !bytes.Equal(first.Body.Bytes(), replay.Body.Bytes()) || len(codes) != 3 {
		t.Fatalf("named closure did not replay the same numeric operation: HTTP %d %s codes=% X", replay.Code, replay.Body, codes)
	}
}

func TestFFD12HTTPClosureAllowsOmittingOptionalReportData(t *testing.T) {
	codes := []byte{}
	api := mixedFFD12API(t, nil, func(_ context.Context, code byte, payload []byte) ([]byte, error) {
		codes = append(codes, code)
		if len(payload) != 0 {
			t.Fatalf("minimal closure command %02X has unexpected payload % X", code, payload)
		}
		if code == 0x28 {
			return []byte{1, 0, 0, 0, 2, 0, 0, 0}, nil
		}
		return nil, nil
	})
	w := ffd12Operation(t, api, "ffd-kkt", "minimal-fn-closure", `[{"cmd":"beginFiscalStorageClosure"},{"cmd":"finalizeFiscalStorageClosure"}]`)
	results := ffd12OperationResults(t, w)
	if !bytes.Equal(codes, []byte{0x14, 0x28}) || len(results) != 2 || results[1].Command != "finalizeFiscalStorageClosure" || len(results[1].Data) != 2 {
		t.Fatalf("minimal closure failed: codes=% X response=%s", codes, w.Body)
	}
}

func TestFFD12HTTPSharedFinalizerKeepsSequentialReportsSeparate(t *testing.T) {
	active := byte(0)
	codes := []byte{}
	api := mixedFFD12API(t, nil, func(_ context.Context, code byte, _ []byte) ([]byte, error) {
		codes = append(codes, code)
		switch code {
		case 0x14, 0x27:
			active = code
		case 0x28:
			switch active {
			case 0x14:
				active = 0
				return []byte{0x2F, 1, 0, 0, 0x94, 1, 0, 0}, nil
			case 0x27:
				active = 0
				return []byte{101, 0, 0, 0, 202, 0, 0, 0, 3, 0, 0, 0, 26, 11, 17}, nil
			default:
				t.Fatal("shared finalizer reached hardware without an active report")
			}
		}
		return nil, nil
	})
	commands := `[{"cmd":"0x27"},{"cmd":"0xE6","params":{"documentAddress":"A","documentPlace":"P"}},{"cmd":"0x28"},{"cmd":"0x14"},{"cmd":"0x17","params":{"documentAddress":"A","documentPlace":"P"}},{"cmd":"0x28"},{"cmd":"beginSettlementReport"},{"cmd":"finalizeSettlementReport"}]`
	w := ffd12Operation(t, api, "ffd-kkt", "sequential-report-types", commands)
	results := ffd12OperationResults(t, w)
	if !bytes.Equal(codes, []byte{0x27, 0xE6, 0x28, 0x14, 0x17, 0x28, 0x27, 0x28}) || len(results) != 8 {
		t.Fatalf("reports did not execute in the requested order: codes=% X response=%s", codes, w.Body)
	}
	for _, index := range []int{2, 7} {
		result := results[index]
		if result.Command != "finalizeSettlementReport" || len(result.Data) != 4 || string(result.Data["documentNumber"]) != "101" || string(result.Data["fiscalSign"]) != "202" || string(result.Data["unconfirmedCount"]) != "3" || string(result.Data["firstUnconfirmedDate"]) != `"2026-11-17"` {
			t.Fatalf("settlement's fifteen-byte layout was lost at step %d: %s", index, w.Body)
		}
	}
	closure := results[5]
	if closure.Command != "finalizeFiscalStorageClosure" || len(closure.Data) != 2 || string(closure.Data["documentNumber"]) != "303" || string(closure.Data["fiscalSign"]) != "404" {
		t.Fatalf("closure result inherited settlement context: %s", w.Body)
	}
}

func TestFFD12HTTPClosureRejectsInvalidWorkflowsBeforeHardware(t *testing.T) {
	api := mixedFFD12API(t, nil, func(context.Context, byte, []byte) ([]byte, error) {
		t.Fatal("invalid closure workflow contacted hardware")
		return nil, nil
	})
	for _, test := range []struct {
		name, commands string
	}{
		{"finalizer without start", `[{"cmd":"0x28"}]`},
		{"incomplete closure", `[{"cmd":"0x14"}]`},
		{"closure data without start", `[{"cmd":"0x17","params":{"documentAddress":"A","documentPlace":"P"}}]`},
		{"named settlement finalizer in closure", `[{"cmd":"0x14"},{"cmd":"finalizeSettlementReport"}]`},
		{"named closure finalizer in settlement", `[{"cmd":"0x27"},{"cmd":"finalizeFiscalStorageClosure"}]`},
		{"nested settlement in closure", `[{"cmd":"0x14"},{"cmd":"0x27"},{"cmd":"0x28"}]`},
		{"nested closure in settlement", `[{"cmd":"0x27"},{"cmd":"0x14"},{"cmd":"0x28"}]`},
		{"closure data in settlement", `[{"cmd":"0x27"},{"cmd":"0x17","params":{"documentAddress":"A","documentPlace":"P"}},{"cmd":"0x28"}]`},
		{"settlement data in closure", `[{"cmd":"0x14"},{"cmd":"0xE6","params":{"documentAddress":"A","documentPlace":"P"}},{"cmd":"0x28"}]`},
		{"data after closure finalizer", `[{"cmd":"0x14"},{"cmd":"0x28"},{"cmd":"0x17","params":{"documentAddress":"A","documentPlace":"P"}}]`},
		{"unpaired report property", `[{"cmd":"0x14"},{"cmd":"0x17","params":{"documentAddress":"A","documentPlace":"P","reportAttribute":"R"}},{"cmd":"0x28"}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			requireAPIError(t, ffd12Operation(t, api, "ffd-kkt", "invalid-fn-closure", test.commands), http.StatusBadRequest, "invalid_request")
		})
	}
	for _, command := range []string{"0x14", "beginFiscalStorageClosure", "0x28", "finalizeFiscalStorageClosure"} {
		requireAPIError(t, ffd12Post(t, api, "ffd-kkt", command, "standalone-fn-closure", `{}`), http.StatusBadRequest, "operation_required")
	}
	for _, command := range []string{"0x17", "sendFiscalStorageClosureData"} {
		requireAPIError(t, ffd12Post(t, api, "ffd-kkt", command, "standalone-fn-closure", `{"documentAddress":"A","documentPlace":"P"}`), http.StatusBadRequest, "operation_required")
	}
}

func TestFFD12HTTPClosureFailuresRemainUnknownAndNeverResend(t *testing.T) {
	for _, test := range []struct {
		name     string
		failCode byte
		response []byte
		err      error
		want     []byte
	}{
		{"data rejected after opening", 0x17, nil, &protocol.DeviceError{Code: 0x11}, []byte{0x14, 0x17}},
		{"finalization response lost", 0x28, nil, io.ErrUnexpectedEOF, []byte{0x14, 0x17, 0x28}},
		{"settlement response is invalid for closure", 0x28, []byte{101, 0, 0, 0, 202, 0, 0, 0, 3, 0, 0, 0, 26, 11, 17}, nil, []byte{0x14, 0x17, 0x28}},
	} {
		t.Run(test.name, func(t *testing.T) {
			codes := []byte{}
			api := mixedFFD12API(t, nil, func(_ context.Context, code byte, _ []byte) ([]byte, error) {
				codes = append(codes, code)
				if code == test.failCode {
					return test.response, test.err
				}
				return nil, nil
			})
			commands := `[{"cmd":"0x14"},{"cmd":"0x17","params":{"documentAddress":"A","documentPlace":"P"}},{"cmd":"0x28"}]`
			first := ffd12Operation(t, api, "ffd-kkt", "uncertain-fn-closure", commands)
			response := requireAPIError(t, first, http.StatusConflict, "outcome_unknown")
			if response.Status != "unknown" || !bytes.Equal(codes, test.want) {
				t.Fatalf("closure did not stop at the failure with an uncertain result: codes=% X response=%s", codes, first.Body)
			}
			replay := ffd12Operation(t, api, "ffd-kkt", "uncertain-fn-closure", strings.ReplaceAll(commands, `"0x28"`, `"finalizeFiscalStorageClosure"`))
			requireAPIError(t, replay, http.StatusConflict, "outcome_unknown")
			lookup := request(t, api, http.MethodGet, "/api/v1/requests?deviceId=ffd-kkt&requestId=uncertain-fn-closure", "", "", "")
			requireAPIError(t, lookup, http.StatusConflict, "outcome_unknown")
			if !bytes.Equal(codes, test.want) {
				t.Fatalf("retry or journal lookup sent an uncertain closure again: % X", codes)
			}
		})
	}
}

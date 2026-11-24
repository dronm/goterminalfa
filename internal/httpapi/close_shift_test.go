package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/dronm/goterminalfa/internal/protocol"
)

func TestCloseShiftIfOpenPronogtiContract(t *testing.T) {
	const fullParams = `{"cashierName":"Иван","cashierInn":"012345678901","suppressPrint":1}`
	const minimalParams = `{"cashierName":"Иван"}`
	nameTLV := []byte{0xFD, 0x03, 4, 0, 0x88, 0xA2, 0xA0, 0xAD}
	innTLV := append([]byte{0xB3, 0x04, 12, 0}, []byte("012345678901")...)
	for _, tc := range []struct {
		name, device, params string
		open                 bool
		printByte            []byte
		withINN              bool
	}{
		{"base already closed", "base-kkt", minimalParams, false, nil, false},
		{"FFD already closed", "ffd-kkt", fullParams, false, nil, true},
		{"base print by default", "base-kkt", minimalParams, true, nil, false},
		{"base print explicitly", "base-kkt", `{"cashierName":"Иван","suppressPrint":0}`, true, []byte{0}, false},
		{"FFD suppress printing", "ffd-kkt", fullParams, true, []byte{1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []byte
			exchange := func(_ context.Context, code byte, payload []byte) ([]byte, error) {
				calls = append(calls, code)
				switch code {
				case 0x20:
					if len(payload) != 0 {
						t.Fatalf("shift query payload = % X", payload)
					}
					if tc.open {
						return []byte{1, 1, 2, 5, 0}, nil
					}
					return []byte{0, 0, 0, 5, 0}, nil
				case 0x29:
					if !bytes.Equal(payload, tc.printByte) {
						t.Fatalf("print flag = % X, want % X", payload, tc.printByte)
					}
				case 0x2F:
					want := bytes.Clone(nameTLV)
					if tc.withINN {
						want = append(want, innTLV...)
					}
					if !bytes.Equal(payload, want) {
						t.Fatalf("cashier TLVs = % X, want % X", payload, want)
					}
				case 0x2A:
					if len(payload) != 0 {
						t.Fatalf("finalization payload = % X", payload)
					}
					report := []byte{1, 2, 7, 0, 0, 0, 9, 0, 0, 0}
					if tc.device == "ffd-kkt" {
						report = append(report, 0x81)
					}
					return report, nil
				default:
					t.Fatalf("unexpected command 0x%02X", code)
				}
				return nil, nil
			}
			api := mixedFFD12API(t, exchange, exchange)
			const id = "pronogti-close-test"
			first := ffd12Post(t, api, tc.device, "closeShiftIfOpen", id, tc.params)
			var got struct {
				DeviceID  string         `json:"deviceId"`
				Command   string         `json:"cmd"`
				RequestID string         `json:"requestId"`
				Status    string         `json:"status"`
				Data      map[string]any `json:"data"`
			}
			if err := json.Unmarshal(first.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			wantCalls := []byte{0x20}
			wantData := map[string]any{"alreadyClosed": true, "shiftNumber": float64(0)}
			if tc.open {
				wantCalls = append(wantCalls, 0x29, 0x2F, 0x2A)
				wantData = map[string]any{"alreadyClosed": false, "shiftNumber": float64(513), "documentNumber": float64(7), "fiscalSign": float64(9)}
				if tc.device == "ffd-kkt" {
					wantData["warningFlags"] = float64(0x81)
				}
			}
			if first.Code != http.StatusOK || got.DeviceID != tc.device || got.Command != "closeShiftIfOpen" || got.RequestID != id || got.Status != "completed" || !reflect.DeepEqual(got.Data, wantData) {
				t.Fatalf("HTTP %d %s, want completed with data %v", first.Code, first.Body, wantData)
			}
			replay := ffd12Post(t, api, tc.device, "closeShiftIfOpen", id, tc.params)
			if replay.Code != first.Code || replay.Header().Get("Idempotency-Replayed") != "true" || !bytes.Equal(replay.Body.Bytes(), first.Body.Bytes()) {
				t.Fatalf("unexpected replay: HTTP %d %s", replay.Code, replay.Body)
			}
			requireAPIError(t, ffd12Post(t, api, tc.device, "closeShiftIfOpen", id, `{"cashierName":"Other"}`), 409, "idempotency_conflict")
			if !bytes.Equal(calls, wantCalls) {
				t.Fatalf("wire commands = % X, want % X", calls, wantCalls)
			}
		})
	}
}

func TestCloseShiftIfOpenRejectsInvalidRequestsBeforeDeviceAccess(t *testing.T) {
	api := mixedFFD12API(t, nil, nil)
	const valid = `{"cashierName":"Cashier"}`
	for _, params := range []string{
		`{}`, `{"cashierName":""}`, `{"cashierName":"🙂"}`,
		`{"cashierName":"` + strings.Repeat("A", 65) + `"}`,
		`{"cashierName":"A","cashierInn":"123"}`, `{"cashierName":"A","cashierInn":null}`,
		`{"cashierName":"A","suppressPrint":true}`, `{"cashierName":"A","suppressPrint":2}`,
		`{"cashierName":"A","suppressPrint":null}`, `{"cashierName":"A","unknown":0}`,
	} {
		requireAPIError(t, ffd12Post(t, api, "ffd-kkt", "closeShiftIfOpen", "invalid", params), 400, "invalid_request")
	}
	for _, device := range []string{"base-kkt", "ffd-kkt"} {
		requireAPIError(t, ffd12Post(t, api, device, "closeShiftIfOpen", "", valid), 400, "invalid_request")
		nested := `[{"cmd":"closeShiftIfOpen","params":` + valid + `}]`
		requireAPIError(t, ffd12Operation(t, api, device, "nested", nested), 400, "invalid_request")
	}
}

func TestCloseShiftIfOpenStopsAndJournalsFailures(t *testing.T) {
	rejected := &protocol.DeviceError{Code: 0x57}
	lost := errors.New("connection lost")
	for _, tc := range []struct {
		name       string
		at         byte
		reply      []byte
		err        error
		httpStatus int
		errorCode  string
	}{
		{"lost status response", 0x20, nil, lost, 502, "transport_error"},
		{"truncated status", 0x20, []byte{1}, nil, 502, "protocol_error"},
		{"unknown shift state", 0x20, []byte{2, 1, 0, 0, 0}, nil, 502, "protocol_error"},
		{"start rejected", 0x29, nil, rejected, 502, "device_error"},
		{"start acknowledgement lost", 0x29, nil, lost, 409, "outcome_unknown"},
		{"cashier rejected after start", 0x2F, nil, rejected, 409, "outcome_unknown"},
		{"finalization rejected after start", 0x2A, nil, rejected, 409, "outcome_unknown"},
		{"finalization response lost", 0x2A, nil, lost, 409, "outcome_unknown"},
		{"FFD report missing warning byte", 0x2A, make([]byte, 10), nil, 409, "outcome_unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []byte
			api := mixedFFD12API(t, nil, func(_ context.Context, code byte, _ []byte) ([]byte, error) {
				calls = append(calls, code)
				if code == tc.at {
					return tc.reply, tc.err
				}
				if code == 0x20 {
					return []byte{1, 42, 0, 0, 0}, nil
				}
				return nil, nil
			})
			const params = `{"cashierName":"A"}`
			first := ffd12Post(t, api, "ffd-kkt", "closeShiftIfOpen", "failure", params)
			requireAPIError(t, first, tc.httpStatus, tc.errorCode)
			replay := ffd12Post(t, api, "ffd-kkt", "closeShiftIfOpen", "failure", params)
			want := []byte{0x20, 0x29, 0x2F, 0x2A}
			want = want[:bytes.IndexByte(want, tc.at)+1]
			if replay.Code != first.Code || !bytes.Equal(replay.Body.Bytes(), first.Body.Bytes()) || !bytes.Equal(calls, want) {
				t.Fatalf("replay HTTP %d %s, commands % X, want % X", replay.Code, replay.Body, calls, want)
			}
			if tc.err == rejected && !strings.Contains(first.Body.String(), "Неверно указан режим налогообложения") {
				t.Fatalf("missing FFD device rejection: %s", first.Body)
			}
		})
	}
}

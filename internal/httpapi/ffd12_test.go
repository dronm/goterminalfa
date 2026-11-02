package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	terminalfa "github.com/dronm/goterminalfa"
	"github.com/dronm/goterminalfa/internal/config"
	"github.com/dronm/goterminalfa/internal/service"
	"github.com/dronm/goterminalfa/internal/store"
	"github.com/sirupsen/logrus"
)

func mixedFFD12API(t *testing.T, baseExchange, ffdExchange fakeExchange) *API {
	t.Helper()
	journal, err := store.Open(filepath.Join(t.TempDir(), "requests.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	unexpected := fakeExchange(func(context.Context, byte, []byte) ([]byte, error) {
		t.Error("unexpected access to a device")
		return nil, fmt.Errorf("unexpected access to a device")
	})
	if baseExchange == nil {
		baseExchange = unexpected
	}
	if ffdExchange == nil {
		ffdExchange = unexpected
	}
	base := service.NewDevice("base-kkt", "serial", "", terminalfa.NewClient(baseExchange), 2)
	base.ProtocolProfile = "base"
	ffd := service.NewDevice("ffd-kkt", "serial", "", terminalfa.NewClient(ffdExchange), 2)
	ffd.ProtocolProfile = "ffd1.2"
	app, err := service.New([]*service.Device{base, ffd}, service.BuiltinCommands(), journal, logger)
	if err != nil {
		t.Fatal(err)
	}
	return New(app, config.HTTP{RequestTimeout: config.Duration(time.Second), MaxBodyBytes: 65536}, logger)
}

func ffd12Post(t *testing.T, api *API, deviceID, command, requestID, params string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(service.Request{
		DeviceID: deviceID, Command: command, RequestID: requestID, Params: json.RawMessage(params),
	})
	if err != nil {
		t.Fatal(err)
	}
	return request(t, api, http.MethodPost, "/api/v1/commands", string(body), "application/json", "")
}

func ffd12Operation(t *testing.T, api *API, deviceID, requestID, commands string) *httptest.ResponseRecorder {
	t.Helper()
	return ffd12Post(t, api, deviceID, "executeOperation", requestID, `{"commands":`+commands+`}`)
}

func ffd12OperationResults(t *testing.T, w *httptest.ResponseRecorder) []struct {
	Command string                     `json:"cmd"`
	Data    map[string]json.RawMessage `json:"data"`
} {
	t.Helper()
	var response struct {
		Status string `json:"status"`
		Data   struct {
			Results []struct {
				Command string                     `json:"cmd"`
				Data    map[string]json.RawMessage `json:"data"`
			} `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || response.Status != "completed" {
		t.Fatalf("operation failed: HTTP %d %s", w.Code, w.Body)
	}
	return response.Data.Results
}

func TestFFD12HTTPSelectsCataloguePerDevice(t *testing.T) {
	api := mixedFFD12API(t, nil, nil)
	for _, test := range []struct {
		path, profile, device string
		count                 int
	}{
		{"/api/v1/commands", "", "", 74},
		{"/api/v1/commands?deviceId=base-kkt", "base", "base-kkt", 74},
		{"/api/v1/commands?deviceId=ffd-kkt", "ffd1.2", "ffd-kkt", 80},
	} {
		t.Run(test.path, func(t *testing.T) {
			w := request(t, api, http.MethodGet, test.path, "", "", "")
			var catalogue struct {
				Commands        []service.CommandDefinition `json:"commands"`
				ProtocolProfile string                      `json:"protocolProfile"`
				DeviceID        string                      `json:"deviceId"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &catalogue); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || len(catalogue.Commands) != test.count || catalogue.ProtocolProfile != test.profile || catalogue.DeviceID != test.device {
				t.Fatalf("unexpected catalogue: HTTP %d profile=%q device=%q count=%d", w.Code, catalogue.ProtocolProfile, catalogue.DeviceID, len(catalogue.Commands))
			}
			byCode := make(map[string]service.CommandDefinition)
			for _, definition := range catalogue.Commands {
				byCode[definition.ProtocolCode] = definition
			}
			if test.profile == "ffd1.2" {
				item, ok := byCode["0x96"]
				if !ok || !item.Implemented || !item.OperationRequired || !item.IdempotencyRequired || item.ProtocolProfile != "ffd1.2" || item.Source.Version != "FFD 1.2" {
					t.Fatalf("FFD item metadata not selected: %+v", item)
				}
				if len(byCode["0x22"].Output) != 4 || len(byCode["0x24"].Output) != 5 || !byCode["0x24"].Output[3].Required || !byCode["0x24"].Output[4].Required {
					t.Fatal("FFD shift warning or receipt timestamp/shift output is missing")
				}
			} else {
				if _, exists := byCode["0x96"]; exists {
					t.Fatal("FFD-only item command leaked into base catalogue")
				}
				if !byCode["0x2B"].Implemented || len(byCode["0x22"].Output) != 3 || len(byCode["0x24"].Output) != 5 || byCode["0x24"].Output[3].Required || byCode["0x24"].Output[4].Required {
					t.Fatal("base wire definitions changed")
				}
			}
		})
	}
	for _, path := range []string{
		"/api/v1/commands?deviceId=", "/api/v1/commands?deviceId=bad%2Fid",
		"/api/v1/commands?deviceId=base-kkt&deviceId=ffd-kkt",
	} {
		requireAPIError(t, request(t, api, http.MethodGet, path, "", "", ""), 400, "invalid_request")
	}
	requireAPIError(t, request(t, api, http.MethodGet, "/api/v1/commands?deviceId=unknown-kkt", "", "", ""), 404, "unknown_device")
	w := request(t, api, http.MethodGet, "/api/v1/devices", "", "", "")
	var listed struct {
		Devices []struct {
			ID              string `json:"id"`
			ProtocolProfile string `json:"protocolProfile"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(listed.Devices) != 2 || listed.Devices[0].ID != "base-kkt" || listed.Devices[0].ProtocolProfile != "base" || listed.Devices[1].ID != "ffd-kkt" || listed.Devices[1].ProtocolProfile != "ffd1.2" {
		t.Fatalf("device profiles not advertised: HTTP %d %s", w.Code, w.Body)
	}
}

func TestFFD12HTTPCompleteShiftWorkflows(t *testing.T) {
	for _, test := range []struct {
		name, commands string
		codes          []byte
	}{
		{"open", `[{"cmd":"0x21","params":{"suppressPrint":1}},{"cmd":"0x2F","params":{"cashierName":"A"}},{"cmd":"0x22"}]`, []byte{0x21, 0x2F, 0x22}},
		{"close", `[{"cmd":"0x29"},{"cmd":"0x2F","params":{"cashierName":"A"}},{"cmd":"0x2A"}]`, []byte{0x29, 0x2F, 0x2A}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var codes []byte
			api := mixedFFD12API(t, nil, func(_ context.Context, code byte, payload []byte) ([]byte, error) {
				codes = append(codes, code)
				switch code {
				case 0x21:
					if !bytes.Equal(payload, []byte{1}) {
						t.Errorf("opening suppression byte: % X", payload)
					}
				case 0x29:
					if len(payload) != 0 {
						t.Errorf("omitted suppression byte was inserted: % X", payload)
					}
				case 0x2F:
					if !bytes.Equal(payload, []byte{0xFD, 0x03, 1, 0, 'A'}) {
						t.Errorf("cashier TLV: % X", payload)
					}
				case 0x22, 0x2A:
					if len(payload) != 0 {
						t.Errorf("shift completion unexpectedly has parameters: % X", payload)
					}
					return []byte{7, 0, 42, 0, 0, 0, 99, 0, 0, 0, 0x20}, nil
				}
				return nil, nil
			})
			first := ffd12Operation(t, api, "ffd-kkt", "shift-"+test.name, test.commands)
			results := ffd12OperationResults(t, first)
			if !bytes.Equal(codes, test.codes) || len(results) != 3 {
				t.Fatalf("wrong workflow sequence: codes=% X body=%s", codes, first.Body)
			}
			data := results[2].Data
			if string(data["shiftNumber"]) != "7" || string(data["documentNumber"]) != "42" || string(data["fiscalSign"]) != "99" || string(data["warningFlags"]) != "32" {
				t.Fatalf("eleven-byte FFD shift response not fully decoded: %s", first.Body)
			}
			replay := ffd12Operation(t, api, "ffd-kkt", "shift-"+test.name, test.commands)
			if replay.Code != 200 || replay.Header().Get("Idempotency-Replayed") != "true" || !bytes.Equal(first.Body.Bytes(), replay.Body.Bytes()) || !bytes.Equal(codes, test.codes) {
				t.Fatalf("FFD shift was not replayed: %d %s", replay.Code, replay.Body)
			}
		})
	}
}

const ffd12HTTPItem = `{"name":"A","priceKopecks":100,"quantity":"1","vatRate":11,"paymentMethod":4,"paymentObject":1}`
const ffd12HTTPPayment = `{"taxSystem":1,"cashKopecks":100,"cashlessKopecks":0,"prepaymentKopecks":0,"creditKopecks":0,"counterProvisionKopecks":0}`

func TestFFD12HTTPUnmarkedReceiptAndProfileIsolation(t *testing.T) {
	var codes []byte
	api := mixedFFD12API(t, nil, func(_ context.Context, code byte, payload []byte) ([]byte, error) {
		codes = append(codes, code)
		switch code {
		case 0x23:
			if !bytes.Equal(payload, []byte{0x45, 0xA0}) {
				t.Errorf("FFD receipt printing parameter is not UINT16 LE: % X", payload)
			}
		case 0x96:
			want := []byte{0x06, 0x04, 1, 0, 'A', 0x37, 0x04, 1, 0, 100, 0xFF, 0x03, 2, 0, 0, 1, 0xAF, 0x04, 1, 0, 11, 0xBE, 0x04, 1, 0, 4, 0xBC, 0x04, 1, 0, 1}
			if !bytes.Equal(payload, want) {
				t.Errorf("FFD item must use flat TLVs with no base item wrapper: got % X want % X", payload, want)
			}
		case 0x24:
			if !bytes.Equal(payload, []byte{1, 100, 0, 0, 0, 0}) {
				t.Errorf("FFD final total must be fixed UINT40 LE: % X", payload)
			}
			return []byte{2, 0, 42, 0, 0, 0, 99, 0, 0, 0, 26, 11, 2, 12, 34, 7, 0}, nil
		}
		return nil, nil
	})
	commands := `[{"cmd":"0x23","params":{"printSuppressionCode":41029}},{"cmd":"0x96","params":` + ffd12HTTPItem + `},{"cmd":"0x2D","params":` + ffd12HTTPPayment + `},{"cmd":"0x24","params":{"calculationSign":1,"totalKopecks":100}}]`
	w := ffd12Operation(t, api, "ffd-kkt", "receipt-1", commands)
	results := ffd12OperationResults(t, w)
	if !bytes.Equal(codes, []byte{0x23, 0x96, 0x2D, 0x24}) || len(results) != 4 {
		t.Fatalf("wrong receipt sequence: codes=% X body=%s", codes, w.Body)
	}
	data := results[3].Data
	if string(data["receiptNumber"]) != "2" || string(data["documentNumber"]) != "42" || string(data["fiscalSign"]) != "99" || string(data["dateTime"]) != `"2026-11-02T12:34"` || string(data["shiftNumber"]) != "7" {
		t.Fatalf("seventeen-byte FFD receipt response not fully decoded: %s", w.Body)
	}
	replay := ffd12Operation(t, api, "ffd-kkt", "receipt-1", commands)
	if replay.Code != 200 || replay.Header().Get("Idempotency-Replayed") != "true" || !bytes.Equal(w.Body.Bytes(), replay.Body.Bytes()) || len(codes) != 4 {
		t.Fatalf("FFD receipt was executed again instead of replayed: %d %s codes=% X", replay.Code, replay.Body, codes)
	}
	requireAPIError(t, ffd12Post(t, api, "base-kkt", "0x96", "", ffd12HTTPItem), 404, "unknown_command")
	requireAPIError(t, ffd12Post(t, api, "ffd-kkt", "0x96", "standalone", ffd12HTTPItem), 400, "operation_required")
	if len(codes) != 4 {
		t.Fatal("rejected standalone item reached hardware")
	}
}

func TestFFD12HTTPLegacyItemBlockedWhileBaseRemainsAvailable(t *testing.T) {
	var codes []byte
	api := mixedFFD12API(t, func(_ context.Context, code byte, _ []byte) ([]byte, error) {
		codes = append(codes, code)
		if code == 0x24 {
			return []byte{2, 0, 42, 0, 0, 0, 99, 0, 0, 0}, nil
		}
		return nil, nil
	}, nil)
	item := `{"item":{"name":"A","priceKopecks":100,"quantity":"1","vatRate":6,"paymentMethod":4}}`
	requireAPIError(t, ffd12Post(t, api, "ffd-kkt", "0x2B", "legacy", item), 501, "command_unavailable")
	commands := `[{"cmd":"0x23"},{"cmd":"0x2B","params":` + item + `},{"cmd":"0x2D","params":` + ffd12HTTPPayment + `},{"cmd":"0x24","params":{"calculationSign":1,"totalKopecks":100}}]`
	w := ffd12Operation(t, api, "base-kkt", "base-receipt", commands)
	results := ffd12OperationResults(t, w)
	if !bytes.Equal(codes, []byte{0x23, 0x2B, 0x2D, 0x24}) || len(results) != 4 || string(results[3].Data["documentNumber"]) != "42" {
		t.Fatalf("base receipt compatibility lost: codes=% X body=%s", codes, w.Body)
	}
}

func TestFFD12HTTPRejectsLegacyLengthFiscalResponsesWithoutRetry(t *testing.T) {
	for _, test := range []struct {
		name, commands string
		finalCode      byte
		response       []byte
	}{
		{"shift base response", `[{"cmd":"0x21"},{"cmd":"0x22"}]`, 0x22, []byte{7, 0, 42, 0, 0, 0, 99, 0, 0, 0}},
		{"receipt base response", `[{"cmd":"0x23"},{"cmd":"0x24","params":{"calculationSign":1,"totalKopecks":100}}]`, 0x24, []byte{2, 0, 42, 0, 0, 0, 99, 0, 0, 0}},
		{"receipt without shift", `[{"cmd":"0x23"},{"cmd":"0x24","params":{"calculationSign":1,"totalKopecks":100}}]`, 0x24, []byte{2, 0, 42, 0, 0, 0, 99, 0, 0, 0, 26, 11, 2, 12, 34}},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			api := mixedFFD12API(t, nil, func(_ context.Context, code byte, _ []byte) ([]byte, error) {
				calls++
				if code == test.finalCode {
					return test.response, nil
				}
				return nil, nil
			})
			first := ffd12Operation(t, api, "ffd-kkt", "malformed-response", test.commands)
			requireAPIError(t, first, 409, "outcome_unknown")
			if calls != 2 {
				t.Fatalf("wrong number of exchanges before malformed response: %d", calls)
			}
			requireAPIError(t, ffd12Operation(t, api, "ffd-kkt", "malformed-response", test.commands), 409, "outcome_unknown")
			if calls != 2 {
				t.Fatalf("uncertain fiscal operation was executed again: %d exchanges", calls)
			}
		})
	}
}

func TestFFD12HTTPShiftReadAndDocumentCancellation(t *testing.T) {
	var codes []byte
	api := mixedFFD12API(t, nil, func(_ context.Context, code byte, payload []byte) ([]byte, error) {
		codes = append(codes, code)
		if len(payload) != 0 {
			t.Errorf("unexpected input to command %02X: % X", code, payload)
		}
		if code == 0x20 {
			return []byte{1, 7, 0, 2, 0}, nil
		}
		return nil, nil
	})
	for i := 0; i < 2; i++ {
		w := ffd12Post(t, api, "ffd-kkt", "0x20", "read-trace", `{}`)
		var response struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || string(response.Data["shiftState"]) != "1" || string(response.Data["shiftNumber"]) != "7" || string(response.Data["receiptNumber"]) != "2" || w.Header().Get("Idempotency-Replayed") != "" {
			t.Fatalf("shift query response: HTTP %d %s", w.Code, w.Body)
		}
	}
	w := ffd12Post(t, api, "ffd-kkt", "0x10", "cancel-1", `{}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"cmd":"cancelDocument"`) {
		t.Fatalf("document cancellation failed: HTTP %d %s", w.Code, w.Body)
	}
	if !bytes.Equal(codes, []byte{0x20, 0x20, 0x10}) {
		t.Fatalf("read was cached or cancellation sent parameters: codes=% X", codes)
	}
}

func TestFFD12HTTPValidSettingsRequireRequestIDAndOldParametersFailBeforeWire(t *testing.T) {
	api := mixedFFD12API(t, nil, nil)
	for _, test := range []struct{ command, params string }{
		{"0x72", `{"dateTime":"2026-11-02T12:34"}`},
		{"0x74", `{"dynamicAddress":0,"ipAddress":"192.0.2.50","subnetMask":"255.255.255.0","gateway":"192.0.2.1"}`},
		{"0x76", `{"domain":"ofd.example","tcpPort":7777,"timerSeconds":30}`},
		{"0x80", `{"controlInterface":0,"tcpPort":7778}`},
	} {
		t.Run(test.command, func(t *testing.T) {
			response := requireAPIError(t, ffd12Post(t, api, "ffd-kkt", test.command, "", test.params), 400, "invalid_request")
			if !strings.Contains(response.Error.Message, "requestId") {
				t.Fatalf("valid settings rejected for wrong reason: %+v", response.Error)
			}
		})
	}
	for i, commands := range []string{
		`[{"cmd":"0x21","params":{"withoutPrinting":1}},{"cmd":"0x22"}]`,
		`[{"cmd":"0x23"},{"cmd":"0x24","params":{"calculationSign":1,"totalKopecks":100,"additionalText":"legacy"}}]`,
		`[{"cmd":"0x23"},{"cmd":"0x96","params":{"item":` + ffd12HTTPItem + `}},{"cmd":"0x24","params":{"calculationSign":1,"totalKopecks":100}}]`,
		`[{"cmd":"0x23"},{"cmd":"0x2D","params":{"taxSystem":1,"cashKopecks":100,"cashlessKopecks":0,"prepaymentKopecks":0,"creditKopecks":0,"counterProvisionKopecks":0,"cashierName":"legacy"}},{"cmd":"0x24","params":{"calculationSign":1,"totalKopecks":100}}]`,
	} {
		requireAPIError(t, ffd12Operation(t, api, "ffd-kkt", fmt.Sprintf("invalid-%d", i), commands), 400, "invalid_request")
	}
}

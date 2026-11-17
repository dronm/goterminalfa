package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dronm/goterminalfa/internal/protocol"
	"github.com/dronm/goterminalfa/internal/service"
)

// RN 0000000001046446 has the independently calculated CRC 46446 for
// sequence 0000000001, user INN 7707083893, and factory serial 550101046182.
const registerKktHTTPParams = `{"reportType":0,"userName":"A","settlementAddress":"B","settlementPlace":"C","representativeName":"D","ofdInn":"7707083893","ofdName":"O","senderEmail":"sender@example.org","userInn":"7707083893","registrationNumber":"0000000001046446","taxationSystems":1}`

func registerKktHTTPChanged(t *testing.T, change func(map[string]any)) string {
	t.Helper()
	var params map[string]any
	if err := json.Unmarshal([]byte(registerKktHTTPParams), &params); err != nil {
		t.Fatal(err)
	}
	change(params)
	body, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func registerKktHTTPResponse(code byte) []byte {
	switch code {
	case 0x02:
		return []byte("550101046182")
	case 0xE3:
		return []byte{42, 0, 0, 0, 99, 0, 0, 0}
	default:
		return nil
	}
}

func TestRegisterKktHTTPCompleteInitialAndReregistrationReplay(t *testing.T) {
	for _, reportType := range []int{0, 1, 2} {
		t.Run(string(rune('0'+reportType)), func(t *testing.T) {
			codes := []byte{}
			params := registerKktHTTPChanged(t, func(params map[string]any) {
				params["reportType"] = reportType
				if reportType != 0 {
					params["reregistrationReason"] = uint64(1 << 31)
				}
			})
			api := mixedFFD12API(t, nil, func(_ context.Context, code byte, payload []byte) ([]byte, error) {
				codes = append(codes, code)
				switch code {
				case 0xE1:
					if !bytes.Equal(payload, []byte{byte(reportType)}) {
						t.Errorf("incorrect report type: % X", payload)
					}
				case 0xE2:
					want := []byte{
						0x18, 0x04, 1, 0, 'A',
						0xF1, 0x03, 1, 0, 'B',
						0xA3, 0x04, 1, 0, 'C',
						0xFD, 0x03, 1, 0, 'D',
						0xF9, 0x03, 12, 0, '7', '7', '0', '7', '0', '8', '3', '8', '9', '3', ' ', ' ',
						0x16, 0x04, 1, 0, 'O',
						0x5D, 0x04, 18, 0, 's', 'e', 'n', 'd', 'e', 'r', '@', 'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 'o', 'r', 'g',
					}
					if !bytes.Equal(payload, want) {
						t.Errorf("registration TLVs or optional omission changed: got % X want % X", payload, want)
					}
				case 0xE3:
					want := append([]byte("7707083893  0000000001046446    "), 1)
					if reportType != 0 {
						want = append(want, 0, 0, 0, 0x80)
					}
					if !bytes.Equal(payload, want) {
						t.Errorf("registration completion must use space padding and optional UINT32LE reason: got % X want % X", payload, want)
					}
				default:
					if code != 0x02 {
						t.Errorf("unexpected command 0x%02X", code)
					}
				}
				return registerKktHTTPResponse(code), nil
			})
			first := ffd12Post(t, api, "ffd-kkt", "registerKkt", "register-1", params)
			var response struct {
				Command string         `json:"cmd"`
				Status  string         `json:"status"`
				Data    map[string]int `json:"data"`
			}
			if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			wantCodes := []byte{0x02, 0xE1, 0xE2, 0xE3}
			if first.Code != http.StatusOK || response.Command != "registerKkt" || response.Status != "completed" || len(response.Data) != 2 || response.Data["documentNumber"] != 42 || response.Data["fiscalSign"] != 99 || !bytes.Equal(codes, wantCodes) {
				t.Fatalf("incorrect registration: HTTP %d %s codes=% X", first.Code, first.Body, codes)
			}
			replay := ffd12Post(t, api, "ffd-kkt", "registerKkt", "register-1", params)
			lookup := request(t, api, http.MethodGet, "/api/v1/requests?deviceId=ffd-kkt&requestId=register-1", "", "", "")
			if replay.Code != http.StatusOK || replay.Header().Get("Idempotency-Replayed") != "true" || !bytes.Equal(first.Body.Bytes(), replay.Body.Bytes()) || !bytes.Equal(first.Body.Bytes(), lookup.Body.Bytes()) || !bytes.Equal(codes, wantCodes) {
				t.Fatalf("registration replay or lookup contacted the device: replay=%s lookup=%s codes=% X", replay.Body, lookup.Body, codes)
			}
			changed := strings.Replace(params, `"userName":"A"`, `"userName":"Changed"`, 1)
			requireAPIError(t, ffd12Post(t, api, "ffd-kkt", "registerKkt", "register-1", changed), http.StatusConflict, "idempotency_conflict")
			if !bytes.Equal(codes, wantCodes) {
				t.Fatalf("conflicting registration contacted the device: % X", codes)
			}
		})
	}
}

func TestRegisterKktHTTPValidationBeforeTraffic(t *testing.T) {
	api := mixedFFD12API(t, nil, func(context.Context, byte, []byte) ([]byte, error) {
		t.Fatal("invalid registration contacted the device")
		return nil, nil
	})
	for _, test := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"invalid user checksum", func(params map[string]any) { params["userInn"] = "7707083894" }},
		{"invalid OFD checksum", func(params map[string]any) { params["ofdInn"] = "7707083894" }},
		{"invalid optional representative checksum", func(params map[string]any) { params["representativeInn"] = "500100732258" }},
		{"invalid sender email", func(params map[string]any) { params["senderEmail"] = "sender@" }},
		{"display name email", func(params map[string]any) { params["senderEmail"] = "Sender <sender@example.org>" }},
		{"short registration number", func(params map[string]any) { params["registrationNumber"] = "000000000104644" }},
		{"missing taxation", func(params map[string]any) { delete(params, "taxationSystems") }},
		{"zero taxation", func(params map[string]any) { params["taxationSystems"] = 0 }},
		{"reserved taxation bit", func(params map[string]any) { params["taxationSystems"] = 8 }},
		{"missing reason", func(params map[string]any) { params["reportType"] = 2 }},
		{"initial reason", func(params map[string]any) { params["reregistrationReason"] = 1 }},
		{"reason exceeds uint32", func(params map[string]any) {
			params["reportType"] = 2
			params["reregistrationReason"] = uint64(1 << 32)
		}},
		{"unpaired attribute", func(params map[string]any) { params["reportAttribute"] = "A" }},
		{"missing OFD without explicit autonomous mode", func(params map[string]any) { delete(params, "ofdInn") }},
		{"unsupported operating bit", func(params map[string]any) { params["operatingMode"] = 4 }},
		{"unknown field", func(params map[string]any) { params["serialNumber"] = "550101046182" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			params := registerKktHTTPChanged(t, test.change)
			requireAPIError(t, ffd12Post(t, api, "ffd-kkt", "registerKkt", "invalid-register", params), http.StatusBadRequest, "invalid_request")
		})
	}
	requireAPIError(t, ffd12Post(t, api, "ffd-kkt", "registerKkt", "", registerKktHTTPParams), http.StatusBadRequest, "invalid_request")
	requireAPIError(t, ffd12Post(t, api, "base-kkt", "registerKkt", "base-register", registerKktHTTPParams), http.StatusNotFound, "unknown_command")
	requireAPIError(t, ffd12Operation(t, api, "ffd-kkt", "nested-register", `[{"cmd":"registerKkt","params":`+registerKktHTTPParams+`}]`), http.StatusBadRequest, "invalid_request")
}

func TestRegisterKktHTTPRuntimeNumberValidationIsDefiniteAndReplayable(t *testing.T) {
	calls := 0
	api := mixedFFD12API(t, nil, func(_ context.Context, code byte, _ []byte) ([]byte, error) {
		calls++
		if code != 0x02 {
			t.Fatalf("invalid registration number reached a mutation: 0x%02X", code)
		}
		return registerKktHTTPResponse(code), nil
	})
	params := strings.Replace(registerKktHTTPParams, "0000000001046446", "0000000001046445", 1)
	first := ffd12Post(t, api, "ffd-kkt", "registerKkt", "invalid-rn", params)
	response := requireAPIError(t, first, http.StatusBadRequest, "invalid_request")
	if response.Status != "failed" || !strings.Contains(response.Error.Message, "registrationNumber") || calls != 1 {
		t.Fatalf("RN validation must precede E1 and remain definite: %s calls=%d", first.Body, calls)
	}
	replay := ffd12Post(t, api, "ffd-kkt", "registerKkt", "invalid-rn", params)
	lookup := request(t, api, http.MethodGet, "/api/v1/requests?deviceId=ffd-kkt&requestId=invalid-rn", "", "", "")
	if replay.Code != http.StatusBadRequest || replay.Header().Get("Idempotency-Replayed") != "true" || !bytes.Equal(first.Body.Bytes(), replay.Body.Bytes()) || !bytes.Equal(first.Body.Bytes(), lookup.Body.Bytes()) || calls != 1 {
		t.Fatalf("invalid RN was retried or became unknown: replay=%s lookup=%s calls=%d", replay.Body, lookup.Body, calls)
	}
}

func TestRegisterKktHTTPFailuresStopAndNeverResend(t *testing.T) {
	for _, test := range []struct {
		name      string
		failCode  byte
		result    []byte
		err       error
		status    int
		errorCode string
		state     string
		want      []byte
	}{
		{"identity read lost", 0x02, nil, io.ErrUnexpectedEOF, http.StatusBadGateway, "transport_error", "failed", []byte{0x02}},
		{"start rejected", 0xE1, nil, &protocol.DeviceError{Code: 0x11}, http.StatusBadGateway, "device_error", "failed", []byte{0x02, 0xE1}},
		{"data rejected", 0xE2, nil, &protocol.DeviceError{Code: 0x11}, http.StatusConflict, "outcome_unknown", "unknown", []byte{0x02, 0xE1, 0xE2}},
		{"completion rejected", 0xE3, nil, &protocol.DeviceError{Code: 0x11}, http.StatusConflict, "outcome_unknown", "unknown", []byte{0x02, 0xE1, 0xE2, 0xE3}},
		{"completion lost", 0xE3, nil, io.ErrUnexpectedEOF, http.StatusConflict, "outcome_unknown", "unknown", []byte{0x02, 0xE1, 0xE2, 0xE3}},
		{"completion truncated", 0xE3, []byte{42, 0, 0, 0}, nil, http.StatusConflict, "outcome_unknown", "unknown", []byte{0x02, 0xE1, 0xE2, 0xE3}},
	} {
		t.Run(test.name, func(t *testing.T) {
			codes := []byte{}
			api := mixedFFD12API(t, nil, func(_ context.Context, code byte, _ []byte) ([]byte, error) {
				codes = append(codes, code)
				if code == test.failCode {
					return test.result, test.err
				}
				return registerKktHTTPResponse(code), nil
			})
			first := ffd12Post(t, api, "ffd-kkt", "registerKkt", "failed-register", registerKktHTTPParams)
			response := requireAPIError(t, first, test.status, test.errorCode)
			if response.Status != test.state || !bytes.Equal(codes, test.want) {
				t.Fatalf("registration did not stop at its failure: %s codes=% X", first.Body, codes)
			}
			replay := ffd12Post(t, api, "ffd-kkt", "registerKkt", "failed-register", registerKktHTTPParams)
			lookup := request(t, api, http.MethodGet, "/api/v1/requests?deviceId=ffd-kkt&requestId=failed-register", "", "", "")
			if replay.Code != test.status || lookup.Code != test.status || !bytes.Equal(codes, test.want) {
				t.Fatalf("failed registration or journal lookup sent more commands: replay=%s lookup=%s codes=% X", replay.Body, lookup.Body, codes)
			}
		})
	}
}

func TestRegisterKktHTTPDiscoveryAndNoLowLevelBypass(t *testing.T) {
	api := mixedFFD12API(t, nil, nil)
	w := request(t, api, http.MethodGet, "/api/v1/commands?deviceId=ffd-kkt", "", "", "")
	var catalogue struct {
		Commands []service.CommandDefinition `json:"commands"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &catalogue); err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]service.CommandDefinition)
	for _, command := range catalogue.Commands {
		byName[command.Name] = command
		for _, code := range []string{"0xE1", "0xE2", "0xE3"} {
			if command.ProtocolCode == code {
				t.Errorf("internal registration step exposed in external catalogue: %+v", command)
			}
		}
	}
	definition := byName["registerKkt"]
	if w.Code != http.StatusOK || definition.Name == "" || !definition.Implemented || !definition.IdempotencyRequired || definition.ReadOnly || definition.OperationRequired || definition.ProtocolCode != "" || definition.ProtocolProfile != "ffd1.2" || definition.RequestLayout != "application" || definition.ResponseLayout != "application" {
		t.Fatalf("registration application discovery is incorrect: %+v", definition)
	}
	for _, code := range []string{"0xE1", "0xE2", "0xE3"} {
		requireAPIError(t, ffd12Post(t, api, "ffd-kkt", code, "no-low-level", `{}`), http.StatusNotFound, "unknown_command")
		requireAPIError(t, ffd12Operation(t, api, "ffd-kkt", "no-static-bypass", `[{"cmd":"`+code+`"}]`), http.StatusBadRequest, "invalid_request")
	}
	for _, name := range []string{"startRegistration", "sendRegistrationData", "completeRegistration"} {
		if byName[name].Implemented || byName[name].UnavailableReason == "" {
			t.Fatalf("superseded base registration command unexpectedly available: %+v", byName[name])
		}
	}
	for _, field := range definition.Input {
		if field.Name == "reregistrationReason" && field.WireType != "UINT32" {
			t.Errorf("reason must advertise UINT32: %+v", field)
		}
	}
}

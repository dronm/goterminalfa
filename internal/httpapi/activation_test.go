package httpapi

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dronm/goterminalfa/internal/service"
)

const ffd12HTTPActivationCode = "00112233445566778899aabbccddeeff"

func TestFFD12HTTPActivationCommandAndAliases(t *testing.T) {
	for _, command := range []string{"applyActivationCode", "0xDD", "0xdd"} {
		t.Run(command, func(t *testing.T) {
			var calls atomic.Int32
			want, err := hex.DecodeString(ffd12HTTPActivationCode)
			if err != nil {
				t.Fatal(err)
			}
			api := mixedFFD12API(t, nil, func(_ context.Context, code byte, payload []byte) ([]byte, error) {
				calls.Add(1)
				if code != 0xDD || !bytes.Equal(payload, want) {
					t.Errorf("activation must send exactly sixteen raw bytes: code=%02X payload=% X", code, payload)
				}
				return nil, nil
			})
			w := ffd12Post(t, api, "ffd-kkt", command, "activation-1", `{"activationCodeHex":"`+ffd12HTTPActivationCode+`"}`)
			var response struct {
				DeviceID  string                     `json:"deviceId"`
				Command   string                     `json:"cmd"`
				RequestID string                     `json:"requestId"`
				Status    string                     `json:"status"`
				Data      map[string]json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusOK || response.DeviceID != "ffd-kkt" || response.Command != "applyActivationCode" || response.RequestID != "activation-1" || response.Status != "completed" || response.Data == nil || len(response.Data) != 0 {
				t.Fatalf("unexpected activation response: HTTP %d %s", w.Code, w.Body)
			}
			if calls.Load() != 1 {
				t.Fatalf("activation sent %d device commands; want only 0xDD", calls.Load())
			}
		})
	}
}

func TestFFD12HTTPActivationRejectsInvalidInputBeforeDeviceAccess(t *testing.T) {
	var calls atomic.Int32
	api := mixedFFD12API(t, nil, func(context.Context, byte, []byte) ([]byte, error) {
		calls.Add(1)
		return nil, nil
	})
	for _, test := range []struct {
		name, params string
	}{
		{"missing code", `{}`},
		{"null code", `{"activationCodeHex":null}`},
		{"numeric code", `{"activationCodeHex":123}`},
		{"byte array", `{"activationCodeHex":[0,1,2,3]}`},
		{"empty code", `{"activationCodeHex":""}`},
		{"fifteen bytes", `{"activationCodeHex":"00112233445566778899aabbccddee"}`},
		{"seventeen bytes", `{"activationCodeHex":"00112233445566778899aabbccddeeff00"}`},
		{"odd hex length", `{"activationCodeHex":"00112233445566778899aabbccddeef"}`},
		{"non-hex character", `{"activationCodeHex":"00112233445566778899aabbccddeefg"}`},
		{"hex prefix", `{"activationCodeHex":"0x00112233445566778899aabbccddeeff"}`},
		{"spaced hex", `{"activationCodeHex":"00 11 22 33 44 55 66 77 88 99 aa bb cc dd ee ff"}`},
		{"unknown parameter", `{"activationCodeHex":"00112233445566778899aabbccddeeff","other":1}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := ffd12Post(t, api, "ffd-kkt", "applyActivationCode", "invalid-activation", test.params)
			requireAPIError(t, w, http.StatusBadRequest, "invalid_request")
		})
	}
	valid := `{"activationCodeHex":"` + ffd12HTTPActivationCode + `"}`
	requireAPIError(t, ffd12Post(t, api, "ffd-kkt", "applyActivationCode", "", valid), http.StatusBadRequest, "invalid_request")
	for _, command := range []string{"applyActivationCode", "0xDD", "0xdd"} {
		requireAPIError(t, ffd12Post(t, api, "base-kkt", command, "base-activation", valid), http.StatusNotFound, "unknown_command")
	}
	if calls.Load() != 0 {
		t.Fatalf("rejected activation requests reached device: %d exchanges", calls.Load())
	}
}

func TestFFD12HTTPActivationReplaysHexCaseAndCommandAliases(t *testing.T) {
	var calls atomic.Int32
	api := mixedFFD12API(t, nil, func(_ context.Context, code byte, payload []byte) ([]byte, error) {
		calls.Add(1)
		want, err := hex.DecodeString(ffd12HTTPActivationCode)
		if err != nil {
			t.Fatal(err)
		}
		if code != 0xDD || !bytes.Equal(payload, want) {
			t.Errorf("unexpected activation request: code=%02X payload=% X", code, payload)
		}
		return nil, nil
	})
	first := ffd12Post(t, api, "ffd-kkt", "applyActivationCode", "activation-replay", `{"activationCodeHex":"`+strings.ToUpper(ffd12HTTPActivationCode)+`"}`)
	if first.Code != http.StatusOK || first.Header().Get("Idempotency-Replayed") != "" {
		t.Fatalf("first activation failed or was unexpectedly replayed: HTTP %d %s", first.Code, first.Body)
	}
	for _, command := range []string{"applyActivationCode", "0xDD", "0xdd"} {
		replay := ffd12Post(t, api, "ffd-kkt", command, "activation-replay", `{"activationCodeHex":"`+ffd12HTTPActivationCode+`"}`)
		if replay.Code != http.StatusOK || replay.Header().Get("Idempotency-Replayed") != "true" || !bytes.Equal(first.Body.Bytes(), replay.Body.Bytes()) {
			t.Fatalf("hex case or command alias did not replay the same activation result: HTTP %d %s", replay.Code, replay.Body)
		}
	}
	different := ffd12Post(t, api, "ffd-kkt", "applyActivationCode", "activation-replay", `{"activationCodeHex":"10112233445566778899aabbccddeeff"}`)
	requireAPIError(t, different, http.StatusConflict, "idempotency_conflict")
	if calls.Load() != 1 {
		t.Fatalf("activation replay or conflicting request reached device again: %d exchanges", calls.Load())
	}
}

func TestFFD12HTTPActivationCatalogue(t *testing.T) {
	api := mixedFFD12API(t, nil, nil)
	w := request(t, api, http.MethodGet, "/api/v1/commands?deviceId=ffd-kkt", "", "", "")
	var catalogue struct {
		Commands []service.CommandDefinition `json:"commands"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &catalogue); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("activation catalogue request failed: HTTP %d %s", w.Code, w.Body)
	}
	for _, command := range catalogue.Commands {
		if command.Name != "applyActivationCode" {
			continue
		}
		if command.ProtocolCode != "0xDD" || command.ProtocolProfile != "ffd1.2" || !command.Implemented || command.ReadOnly || !command.IdempotencyRequired || command.OperationRequired || len(command.Output) != 0 {
			t.Fatalf("activation command metadata does not describe a standalone FFD mutation: %+v", command)
		}
		if len(command.Input) != 1 || command.Input[0].Name != "activationCodeHex" || !command.Input[0].Required || command.Input[0].FixedLength != 16 {
			t.Fatalf("activation parameter does not require sixteen raw bytes: %+v", command.Input)
		}
		return
	}
	t.Fatal("FFD activation command is missing from the device catalogue")
}

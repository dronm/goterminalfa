package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func ffd12HTTPHostSettings(code byte) []byte {
	if code == 0x83 {
		return []byte{0x62, 0x75, 1, 0, 'h', 0x63, 0x75, 2, 0, 0xBB, 1, 0x64, 0x75, 2, 0, 30, 0}
	}
	return []byte{0x6C, 0x75, 1, 0, 'h', 0x6D, 0x75, 2, 0, 0xBB, 1}
}

func TestFFD12HTTPDiagnosticReadsAreFreshAndDoNotRequireRequestIDs(t *testing.T) {
	for _, test := range []struct {
		name, alias, data string
		code              byte
		response          []byte
	}{
		{"getOismSettings", "0x83", `{"host":"h","tcpPort":443,"timeoutSeconds":30}`, 0x83, ffd12HTTPHostSettings(0x83)},
		{"getMarkingKeyUpdateSettings", "0x85", `{"host":"h","tcpPort":443}`, 0x85, ffd12HTTPHostSettings(0x85)},
		{"getFiscalStorageFormat", "0x0F", `{"ffdVersion":4,"supportedFfdVersion":4}`, 0x0F, []byte{4, 4}},
		{"getOismNotificationStatus", "0x9E", `{"state":0,"queuedNotifications":0,"currentNotificationNumber":0,"currentNotificationDateTime":null,"storageUsedPercent":0}`, 0x9E, make([]byte, 13)},
		{"getFiscalStorageMemory", "0x9D", `{"remainingLongTermDocuments":1,"freeShortTermStorageKB":2}`, 0x9D, []byte{1, 0, 0, 0, 2, 0, 0, 0}},
		{"getServiceParameters", "0xA2", `{"parameters":"opaque=1\r\n"}`, 0xA2, []byte("opaque=1\r\n")},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			api := mixedFFD12API(t, nil, func(_ context.Context, code byte, payload []byte) ([]byte, error) {
				calls++
				if code != test.code || len(payload) != 0 {
					t.Fatalf("unexpected read request: command=%02X payload=% X", code, payload)
				}
				return test.response, nil
			})
			for index, command := range []string{test.name, test.alias, strings.ToLower(test.alias)} {
				requestID := "read-trace"
				if index == 0 {
					requestID = ""
				}
				w := ffd12Post(t, api, "ffd-kkt", command, requestID, `{}`)
				var response struct {
					Command string         `json:"cmd"`
					Status  string         `json:"status"`
					Data    map[string]any `json:"data"`
				}
				var expected map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(test.data), &expected); err != nil {
					t.Fatal(err)
				}
				if w.Code != http.StatusOK || response.Command != test.name || response.Status != "completed" || !reflect.DeepEqual(response.Data, expected) || w.Header().Get("Idempotency-Replayed") != "" {
					t.Fatalf("unexpected read response: HTTP %d %s", w.Code, w.Body)
				}
			}
			lookup := request(t, api, http.MethodGet, "/api/v1/requests?deviceId=ffd-kkt&requestId=read-trace", "", "", "")
			requireAPIError(t, lookup, http.StatusNotFound, "not_found")
			requireAPIError(t, ffd12Post(t, api, "ffd-kkt", test.name, "", `{"unexpected":1}`), http.StatusBadRequest, "invalid_request")
			requireAPIError(t, ffd12Post(t, api, "base-kkt", test.alias, "", `{}`), http.StatusNotFound, "unknown_command")
			if calls != 3 {
				t.Fatalf("reads must query freshly while rejected inputs send nothing: got %d exchanges", calls)
			}
		})
	}
}

func TestFFD12HTTPHostSettingWritesRequireIDsAndReplay(t *testing.T) {
	for _, test := range []struct {
		name, alias, params, changed string
		code                         byte
	}{
		{"setOismSettings", "0x82", `{"host":"h","tcpPort":443,"timeoutSeconds":30}`, `{"host":"h","tcpPort":444,"timeoutSeconds":30}`, 0x82},
		{"setMarkingKeyUpdateSettings", "0x84", `{"host":"h","tcpPort":443}`, `{"host":"h","tcpPort":444}`, 0x84},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			api := mixedFFD12API(t, nil, func(_ context.Context, code byte, payload []byte) ([]byte, error) {
				calls++
				if code != test.code || !bytes.Equal(payload, ffd12HTTPHostSettings(code+1)) {
					t.Fatalf("unexpected setting write: command=%02X payload=% X", code, payload)
				}
				return nil, nil
			})
			requireAPIError(t, ffd12Post(t, api, "ffd-kkt", test.name, "", test.params), http.StatusBadRequest, "invalid_request")
			requireAPIError(t, ffd12Post(t, api, "ffd-kkt", test.name, "invalid-settings", `{"host":"h"}`), http.StatusBadRequest, "invalid_request")
			requireAPIError(t, ffd12Post(t, api, "base-kkt", test.alias, "base-settings", test.params), http.StatusNotFound, "unknown_command")
			if calls != 0 {
				t.Fatal("invalid or base-profile settings requests reached a device")
			}
			first := ffd12Post(t, api, "ffd-kkt", test.alias, "settings-write", test.params)
			if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"data":{}`) {
				t.Fatalf("setting write failed: HTTP %d %s", first.Code, first.Body)
			}
			replay := ffd12Post(t, api, "ffd-kkt", test.name, "settings-write", test.params)
			if replay.Code != http.StatusOK || replay.Header().Get("Idempotency-Replayed") != "true" || !bytes.Equal(first.Body.Bytes(), replay.Body.Bytes()) {
				t.Fatalf("setting write was not replayed: HTTP %d %s", replay.Code, replay.Body)
			}
			requireAPIError(t, ffd12Post(t, api, "ffd-kkt", test.name, "settings-write", test.changed), http.StatusConflict, "idempotency_conflict")
			lookup := request(t, api, http.MethodGet, "/api/v1/requests?deviceId=ffd-kkt&requestId=settings-write", "", "", "")
			if lookup.Code != http.StatusOK || !bytes.Equal(first.Body.Bytes(), lookup.Body.Bytes()) || calls != 1 {
				t.Fatalf("setting write journal or send count is incorrect: HTTP %d %s calls=%d", lookup.Code, lookup.Body, calls)
			}
		})
	}
}

func TestFFD12HTTPHostSettingsCanBeWrittenAndReadInOneOperation(t *testing.T) {
	codes := []byte{0x82, 0x84, 0x83, 0x85}
	calls := 0
	api := mixedFFD12API(t, nil, func(_ context.Context, code byte, payload []byte) ([]byte, error) {
		if calls >= len(codes) || code != codes[calls] {
			t.Fatalf("unexpected host-settings operation command %02X at index %d", code, calls)
		}
		calls++
		if code == 0x82 || code == 0x84 {
			if !bytes.Equal(payload, ffd12HTTPHostSettings(code+1)) {
				t.Fatalf("incorrect host settings payload: % X", payload)
			}
			return nil, nil
		}
		if len(payload) != 0 {
			t.Fatalf("settings read has unexpected parameters: % X", payload)
		}
		return ffd12HTTPHostSettings(code), nil
	})
	invalid := `[{"cmd":"setOismSettings","params":{"host":"h","tcpPort":443,"timeoutSeconds":30}},{"cmd":"setMarkingKeyUpdateSettings","params":{"host":"h","tcpPort":65536}}]`
	requireAPIError(t, ffd12Operation(t, api, "ffd-kkt", "invalid-host-batch", invalid), http.StatusBadRequest, "invalid_request")
	if calls != 0 {
		t.Fatal("a later invalid setting was not rejected before the first write")
	}
	commands := `[{"cmd":"0x82","params":{"host":"h","tcpPort":443,"timeoutSeconds":30}},{"cmd":"0x84","params":{"host":"h","tcpPort":443}},{"cmd":"0x83"},{"cmd":"0x85"}]`
	first := ffd12Operation(t, api, "ffd-kkt", "host-batch", commands)
	results := ffd12OperationResults(t, first)
	if len(results) != 4 || calls != 4 || results[2].Command != "getOismSettings" || results[3].Command != "getMarkingKeyUpdateSettings" {
		t.Fatalf("host settings operation did not complete: %s calls=%d", first.Body, calls)
	}
	replay := ffd12Operation(t, api, "ffd-kkt", "host-batch", commands)
	if replay.Header().Get("Idempotency-Replayed") != "true" || !bytes.Equal(first.Body.Bytes(), replay.Body.Bytes()) || calls != 4 {
		t.Fatalf("host settings operation was not replayed: %s calls=%d", replay.Body, calls)
	}
}

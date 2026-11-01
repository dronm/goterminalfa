package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestStatusChecksIdentityWithoutAnotherCommand(t *testing.T) {
	response := append([]byte("550101046182"), []byte{26, 11, 1, 12, 34, 0, 0, 1, 2}...)
	for _, expected := range []string{"550101046182", "000000000001"} {
		t.Run(expected, func(t *testing.T) {
			calls := 0
			api := testAPI(t, expected, func(_ context.Context, command byte, data []byte) ([]byte, error) {
				calls++
				if command != 0x01 || len(data) != 0 {
					t.Fatalf("unexpected command %02X %X", command, data)
				}
				return response, nil
			}, nil)
			w := request(t, api, http.MethodPost, "/api/v1/commands", `{"deviceId":"cashdesk-1","cmd":"0x01"}`, "application/json", "")
			if expected == "550101046182" {
				if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"serialNumber":"550101046182"`) {
					t.Fatalf("status: %d %s", w.Code, w.Body)
				}
			} else {
				requireAPIError(t, w, 502, "device_identity_mismatch")
			}
			if calls != 1 {
				t.Fatalf("read-only status sent %d commands", calls)
			}
		})
	}
}

func TestAllUnresolvedCommandsFailWithoutDeviceAccess(t *testing.T) {
	api := testAPI(t, "", func(context.Context, byte, []byte) ([]byte, error) {
		t.Fatal("unresolved command reached hardware")
		return nil, nil
	}, nil)
	for _, command := range []string{"0x14", "0x15", "0x17", "0x34"} {
		body, _ := json.Marshal(map[string]any{"deviceId": "cashdesk-1", "cmd": command})
		w := request(t, api, http.MethodPost, "/api/v1/commands", string(body), "application/json", "")
		requireAPIError(t, w, 501, "command_unavailable")
	}
}

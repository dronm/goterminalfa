package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	terminalfa "github.com/dronm/goterminalfa"
	"github.com/dronm/goterminalfa/internal/config"
	"github.com/dronm/goterminalfa/internal/protocol"
	"github.com/dronm/goterminalfa/internal/service"
	"github.com/dronm/goterminalfa/internal/store"
	"github.com/sirupsen/logrus"
)

type fakeExchange func(context.Context, byte, []byte) ([]byte, error)

func (f fakeExchange) Exchange(ctx context.Context, command byte, payload []byte) ([]byte, error) {
	return f(ctx, command, payload)
}

func testAPI(t *testing.T, expectedSerial string, exchange fakeExchange, commands []service.Command) *API {
	t.Helper()
	journal, err := store.Open(filepath.Join(t.TempDir(), "requests.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	if exchange == nil {
		exchange = func(context.Context, byte, []byte) ([]byte, error) {
			return []byte("550101046182"), nil
		}
	}
	device := service.NewDevice("cashdesk-1", "serial", expectedSerial, terminalfa.NewClient(exchange), 2)
	if commands == nil {
		commands = service.BuiltinCommands()
	}
	s, err := service.New([]*service.Device{device}, commands, journal, logger)
	if err != nil {
		t.Fatal(err)
	}
	return New(s, config.HTTP{RequestTimeout: config.Duration(time.Second), MaxBodyBytes: 4096}, logger)
}

func request(t *testing.T, handler http.Handler, method, path, body, contentType, token string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		r.Header.Set("Authorization", token)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if got := w.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if !json.Valid(w.Body.Bytes()) {
		t.Fatalf("response is not valid JSON: HTTP %d %q", w.Code, w.Body.String())
	}
	return w
}

func requireAPIError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) service.Response {
	t.Helper()
	var response service.Response
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != status || response.Error == nil || response.Error.Code != code {
		t.Fatalf("got HTTP %d %s; want HTTP %d error %q", w.Code, w.Body, status, code)
	}
	return response
}

func TestSerialNumberCommandAndAlias(t *testing.T) {
	for _, command := range []string{"getSerialNumber", "ID"} {
		t.Run(command, func(t *testing.T) {
			var calls atomic.Int32
			a := testAPI(t, "550101046182", func(_ context.Context, code byte, payload []byte) ([]byte, error) {
				calls.Add(1)
				if code != 0x02 || len(payload) != 0 {
					t.Errorf("device request: code %02X, data % X", code, payload)
				}
				return []byte("550101046182"), nil
			}, nil)
			body := fmt.Sprintf(`{"deviceId":"cashdesk-1","cmd":%q,"params":{},"requestId":"trace-1"}`, command)
			w := request(t, a, http.MethodPost, "/api/v1/commands", body, "application/json; charset=utf-8", "")
			var response struct {
				DeviceID  string                     `json:"deviceId"`
				Command   string                     `json:"cmd"`
				RequestID string                     `json:"requestId"`
				Status    string                     `json:"status"`
				Data      service.SerialNumberResult `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || response.DeviceID != "cashdesk-1" || response.Command != "getSerialNumber" || response.RequestID != "trace-1" || response.Status != "completed" || response.Data.SerialNumber != "550101046182" {
				t.Fatalf("unexpected response: HTTP %d %s", w.Code, w.Body)
			}
			if calls.Load() != 1 {
				t.Fatalf("device exchanges = %d", calls.Load())
			}
		})
	}
}

func TestRejectsMalformedAndAmbiguousJSONBeforeDeviceAccess(t *testing.T) {
	var calls atomic.Int32
	a := testAPI(t, "", func(context.Context, byte, []byte) ([]byte, error) {
		calls.Add(1)
		return []byte("550101046182"), nil
	}, nil)
	cases := []struct {
		name    string
		body    string
		message string
	}{
		{"syntax", `{"deviceId":"cashdesk-1","cmd":"ID"`, ""},
		{"empty", ``, ""},
		{"null envelope", `null`, ""},
		{"array envelope", `[]`, ""},
		{"scalar envelope", `42`, ""},
		{"missing device", `{"cmd":"ID"}`, ""},
		{"missing command", `{"deviceId":"cashdesk-1"}`, ""},
		{"duplicate envelope field", `{"deviceId":"cashdesk-1","cmd":"ID","cmd":"getSerialNumber"}`, "duplicate"},
		{"duplicate params field", `{"deviceId":"cashdesk-1","cmd":"ID","params":{},"params":{}}`, "duplicate"},
		{"duplicate nested field", `{"deviceId":"cashdesk-1","cmd":"ID","params":{"x":1,"x":2}}`, "duplicate"},
		{"unknown envelope field", `{"deviceId":"cashdesk-1","cmd":"ID","par1":1}`, ""},
		{"case variant envelope", `{"deviceId":"cashdesk-1","Cmd":"ID"}`, ""},
		{"semantic duplicate field", `{"deviceId":"cashdesk-1","DeviceID":"other","cmd":"ID"}`, ""},
		{"unknown command parameter", `{"deviceId":"cashdesk-1","cmd":"ID","params":{"par1":1}}`, ""},
		{"null params", `{"deviceId":"cashdesk-1","cmd":"ID","params":null}`, ""},
		{"array params", `{"deviceId":"cashdesk-1","cmd":"ID","params":[]}`, ""},
		{"string params", `{"deviceId":"cashdesk-1","cmd":"ID","params":""}`, ""},
		{"second JSON object", `{"deviceId":"cashdesk-1","cmd":"ID"} {}`, ""},
		{"trailing garbage", `{"deviceId":"cashdesk-1","cmd":"ID"} trailing`, ""},
		{"excessive nesting", `{"deviceId":"cashdesk-1","cmd":"ID","params":{"x":` + strings.Repeat("[", 70) + "0" + strings.Repeat("]", 70) + "}}", "nesting"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := request(t, a, http.MethodPost, "/api/v1/commands", tc.body, "application/json", "")
			response := requireAPIError(t, w, 400, "invalid_request")
			if tc.message != "" && !strings.Contains(response.Error.Message, tc.message) {
				t.Fatalf("message %q does not contain %q", response.Error.Message, tc.message)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid requests reached device: %d exchanges", calls.Load())
	}
}

func TestHTTPContentTypeAndBodyLimit(t *testing.T) {
	a := testAPI(t, "", nil, nil)
	body := `{"deviceId":"cashdesk-1","cmd":"ID"}`
	for _, contentType := range []string{"", "text/plain", "application/json-seq", "application/json; broken"} {
		t.Run("content type "+contentType, func(t *testing.T) {
			w := request(t, a, http.MethodPost, "/api/v1/commands", body, contentType, "")
			requireAPIError(t, w, 415, "invalid_request")
		})
	}
	a.options.MaxBodyBytes = int64(len(body) - 1)
	w := request(t, a, http.MethodPost, "/api/v1/commands", body, "application/json", "")
	requireAPIError(t, w, 413, "invalid_request")
	a.options.MaxBodyBytes = int64(len(body))
	w = request(t, a, http.MethodPost, "/api/v1/commands", body, "application/json", "")
	if w.Code != 200 {
		t.Fatalf("body exactly at limit rejected: HTTP %d %s", w.Code, w.Body)
	}
}

func TestBearerTokenAndUnauthenticatedHealth(t *testing.T) {
	var calls atomic.Int32
	a := testAPI(t, "", func(context.Context, byte, []byte) ([]byte, error) {
		calls.Add(1)
		return []byte("550101046182"), nil
	}, nil)
	a.options.BearerToken = "test-secret"
	for _, path := range []string{"/api/v1/devices", "/api/v1/commands", "/api/v1/requests?deviceId=cashdesk-1&requestId=trace-1"} {
		for _, token := range []string{"", "Bearer wrong", "Basic test-secret"} {
			w := request(t, a, http.MethodGet, path, "", "", token)
			requireAPIError(t, w, 401, "unauthorized")
			if got := w.Header().Get("WWW-Authenticate"); got != "Bearer" {
				t.Fatalf("WWW-Authenticate = %q", got)
			}
		}
	}
	w := request(t, a, http.MethodGet, "/healthz", "", "", "")
	if w.Code != 200 || w.Body.String() != `{"status":"ok"}` {
		t.Fatalf("health response: HTTP %d %s", w.Code, w.Body)
	}
	w = request(t, a, http.MethodPost, "/api/v1/commands", `{"deviceId":"cashdesk-1","cmd":"ID"}`, "application/json", "Bearer test-secret")
	if w.Code != 200 || calls.Load() != 1 {
		t.Fatalf("authenticated request: HTTP %d %s; device calls %d", w.Code, w.Body, calls.Load())
	}
}

func TestRoutesMethodsAndDiscovery(t *testing.T) {
	var calls atomic.Int32
	a := testAPI(t, "550101046182", func(context.Context, byte, []byte) ([]byte, error) {
		calls.Add(1)
		return []byte("550101046182"), nil
	}, nil)
	cases := []struct {
		method, path, allow string
	}{
		{http.MethodPost, "/healthz", "GET"},
		{http.MethodPost, "/api/v1/devices", "GET"},
		{http.MethodPut, "/api/v1/commands", "GET, POST"},
		{http.MethodPost, "/api/v1/requests", "GET"},
	}
	for _, tc := range cases {
		w := request(t, a, tc.method, tc.path, "", "", "")
		requireAPIError(t, w, 405, "method_not_allowed")
		if got := w.Header().Get("Allow"); got != tc.allow {
			t.Fatalf("Allow for %s = %q, want %q", tc.path, got, tc.allow)
		}
	}
	w := request(t, a, http.MethodGet, "/does-not-exist", "", "", "")
	requireAPIError(t, w, 404, "not_found")
	w = request(t, a, http.MethodGet, "/api/v1/commands", "", "", "")
	var catalogue struct {
		Commands []service.CommandDefinition `json:"commands"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &catalogue); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(catalogue.Commands) != 74 || catalogue.Commands[0].Name != "getStatus" || catalogue.Commands[0].ProtocolCode != "0x01" || catalogue.Commands[0].Source.Page != 10 || !catalogue.Commands[0].ReadOnly || catalogue.Commands[0].IdempotencyRequired {
		t.Fatalf("command catalogue: HTTP %d, %d definitions", w.Code, len(catalogue.Commands))
	}
	w = request(t, a, http.MethodGet, "/api/v1/devices", "", "", "")
	var devices struct {
		Devices []service.DeviceInfo `json:"devices"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &devices); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(devices.Devices) != 1 || devices.Devices[0].ID != "cashdesk-1" || devices.Devices[0].ConnectionType != "serial" {
		t.Fatalf("devices: HTTP %d %s", w.Code, w.Body)
	}
	if calls.Load() != 0 {
		t.Fatalf("routing/discovery sent %d device commands", calls.Load())
	}
}

func TestReadonlyRequestsAreNotPersistedOrReplayed(t *testing.T) {
	var calls atomic.Int32
	a := testAPI(t, "", func(context.Context, byte, []byte) ([]byte, error) {
		calls.Add(1)
		return []byte("550101046182"), nil
	}, nil)
	for i := 0; i < 2; i++ {
		w := request(t, a, http.MethodPost, "/api/v1/commands", `{"deviceId":"cashdesk-1","cmd":"ID","requestId":"trace-1"}`, "application/json", "")
		if w.Code != 200 || w.Header().Get("Idempotency-Replayed") != "" {
			t.Fatalf("read-only response: HTTP %d %s", w.Code, w.Body)
		}
	}
	w := request(t, a, http.MethodGet, "/api/v1/requests?deviceId=cashdesk-1&requestId=trace-1", "", "", "")
	requireAPIError(t, w, 404, "not_found")
	if calls.Load() != 2 {
		t.Fatalf("read-only device calls = %d, want two fresh queries", calls.Load())
	}
}

func TestCommandExecutionErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		expected string
		data     []byte
		err      error
		status   int
		code     string
	}{
		{"device error", "", nil, &protocol.DeviceError{Code: 0x30}, 502, "device_error"},
		{"CRC error", "", nil, fmt.Errorf("read failed: %w", protocol.ErrChecksumMismatch), 502, "protocol_error"},
		{"frame error", "", nil, protocol.ErrMalformedFrame, 502, "protocol_error"},
		{"invalid serial", "", []byte("123"), nil, 502, "protocol_error"},
		{"identity mismatch", "000000000001", []byte("550101046182"), nil, 502, "device_identity_mismatch"},
		{"transport failure", "", nil, errors.New("sensitive operating system detail"), 502, "transport_error"},
		{"timeout", "", nil, context.DeadlineExceeded, 504, "device_timeout"},
		{"cancel", "", nil, context.Canceled, 504, "device_timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := testAPI(t, tc.expected, func(context.Context, byte, []byte) ([]byte, error) { return tc.data, tc.err }, nil)
			w := request(t, a, http.MethodPost, "/api/v1/commands", `{"deviceId":"cashdesk-1","cmd":"ID"}`, "application/json", "")
			response := requireAPIError(t, w, tc.status, tc.code)
			if response.Status != "failed" {
				t.Fatalf("read-only failure status = %q", response.Status)
			}
			if tc.code == "device_error" && (response.Error.DeviceCode == nil || *response.Error.DeviceCode != 0x30) {
				t.Fatalf("missing device error code: %s", w.Body)
			}
			if tc.code == "transport_error" && strings.Contains(w.Body.String(), "sensitive operating system detail") {
				t.Fatal("internal transport error exposed to client")
			}
		})
	}
}

func TestHTTPDeadlineReachesDeviceExchange(t *testing.T) {
	a := testAPI(t, "", func(ctx context.Context, _ byte, _ []byte) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}, nil)
	a.options.RequestTimeout = config.Duration(20 * time.Millisecond)
	w := request(t, a, http.MethodPost, "/api/v1/commands", `{"deviceId":"cashdesk-1","cmd":"ID"}`, "application/json", "")
	requireAPIError(t, w, 504, "device_timeout")
}

func TestUnknownDeviceAndCommandDoNotReachTransport(t *testing.T) {
	var calls atomic.Int32
	a := testAPI(t, "", func(context.Context, byte, []byte) ([]byte, error) {
		calls.Add(1)
		return []byte("550101046182"), nil
	}, nil)
	w := request(t, a, http.MethodPost, "/api/v1/commands", `{"deviceId":"missing","cmd":"ID"}`, "application/json", "")
	requireAPIError(t, w, 404, "unknown_device")
	w = request(t, a, http.MethodPost, "/api/v1/commands", `{"deviceId":"cashdesk-1","cmd":"0x11"}`, "application/json", "")
	requireAPIError(t, w, 404, "unknown_command")
	if calls.Load() != 0 {
		t.Fatalf("unknown requests sent %d device commands", calls.Load())
	}
}

func TestIdempotencyReplayHeaderForRegisteredMutation(t *testing.T) {
	// This test-only mutation verifies the HTTP replay boundary. The executable
	// registers the documented commands rather than this synthetic handler.
	var mutation service.Command
	for _, command := range service.BuiltinCommands() {
		if command.Definition.Name == "getSerialNumber" {
			mutation = command
			break
		}
	}
	if mutation.Execute == nil {
		t.Fatal("serial-number command is missing")
	}
	mutation.Definition.Name = "testMutation"
	mutation.Definition.Aliases = nil
	mutation.Definition.ReadOnly = false
	mutation.Definition.IdempotencyRequired = true
	var calls atomic.Int32
	a := testAPI(t, "", func(context.Context, byte, []byte) ([]byte, error) {
		calls.Add(1)
		return []byte("550101046182"), nil
	}, []service.Command{mutation})
	body := `{"deviceId":"cashdesk-1","cmd":"testMutation","requestId":"mutation-1","params":{}}`
	first := request(t, a, http.MethodPost, "/api/v1/commands", body, "application/json", "")
	second := request(t, a, http.MethodPost, "/api/v1/commands", body, "application/json", "")
	if first.Code != 200 || second.Code != 200 || first.Body.String() != second.Body.String() || first.Header().Get("Idempotency-Replayed") != "" || second.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("first HTTP %d %s; second HTTP %d %s; replay header %q", first.Code, first.Body, second.Code, second.Body, second.Header().Get("Idempotency-Replayed"))
	}
	lookup := request(t, a, http.MethodGet, "/api/v1/requests?deviceId=cashdesk-1&requestId=mutation-1", "", "", "")
	if lookup.Code != 200 || lookup.Body.String() != first.Body.String() || lookup.Header().Get("Idempotency-Replayed") != "true" || calls.Load() != 1 {
		t.Fatalf("lookup HTTP %d %s; device calls %d", lookup.Code, lookup.Body, calls.Load())
	}
}

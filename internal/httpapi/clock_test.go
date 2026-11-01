package httpapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/dronm/goterminalfa/internal/service"
)

func TestSyncDateTimeAPIUsesServerClockAndReplaysResult(t *testing.T) {
	var commands []byte
	var clockPayload []byte
	a := testAPI(t, "550101046182", func(_ context.Context, code byte, payload []byte) ([]byte, error) {
		commands = append(commands, code)
		switch code {
		case 0x02:
			if len(payload) != 0 {
				t.Fatalf("identity query payload: % X", payload)
			}
			return []byte("550101046182"), nil
		case 0x72:
			clockPayload = append([]byte(nil), payload...)
			return nil, nil
		default:
			t.Fatalf("unexpected command 0x%02X", code)
			return nil, nil
		}
	}, nil)
	body := `{"deviceId":"cashdesk-1","cmd":"syncDateTime","requestId":"clock-sync-1","params":{}}`
	before := time.Now()
	first := request(t, a, http.MethodPost, "/api/v1/commands", body, "application/json", "")
	after := time.Now()
	var response struct {
		DeviceID  string `json:"deviceId"`
		Command   string `json:"cmd"`
		RequestID string `json:"requestId"`
		Status    string `json:"status"`
		Data      struct {
			DateTime string `json:"dateTime"`
		} `json:"data"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if first.Code != http.StatusOK || response.Status != "completed" || response.DeviceID != "cashdesk-1" || response.Command != "syncDateTime" || response.RequestID != "clock-sync-1" {
		t.Fatalf("sync response: HTTP %d %s", first.Code, first.Body)
	}
	const layout = "2006-01-02T15:04"
	if response.Data.DateTime != before.Format(layout) && response.Data.DateTime != after.Format(layout) {
		t.Fatalf("returned dateTime %q is outside server-local execution bounds %q..%q", response.Data.DateTime, before.Format(layout), after.Format(layout))
	}
	parsed, err := time.Parse(layout, response.Data.DateTime)
	if err != nil || len(response.Data.DateTime) != 16 {
		t.Fatalf("dateTime must have minute precision and no timezone: %q (%v)", response.Data.DateTime, err)
	}
	if !bytes.Equal(commands, []byte{0x02, 0x72}) {
		t.Fatalf("expected identity verification then clock setter, got % X", commands)
	}
	if len(clockPayload) != 9 || binary.LittleEndian.Uint16(clockPayload[:2]) != 30000 || binary.LittleEndian.Uint16(clockPayload[2:4]) != 5 {
		t.Fatalf("expected one DATETIME5 TLV with tag 30000, got % X", clockPayload)
	}
	wantCalendar := []byte{byte(parsed.Year() - 2000), byte(parsed.Month()), byte(parsed.Day()), byte(parsed.Hour()), byte(parsed.Minute())}
	if !bytes.Equal(clockPayload[4:], wantCalendar) {
		t.Fatalf("wire calendar % X differs from returned dateTime %q (% X)", clockPayload[4:], response.Data.DateTime, wantCalendar)
	}
	if first.Header().Get("Idempotency-Replayed") != "" {
		t.Fatal("first synchronization was marked as replayed")
	}

	second := request(t, a, http.MethodPost, "/api/v1/commands", body, "application/json", "")
	if second.Code != http.StatusOK || second.Body.String() != first.Body.String() || second.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("sync target/result was not replayed: first %s, second HTTP %d %s", first.Body, second.Code, second.Body)
	}
	lookup := request(t, a, http.MethodGet, "/api/v1/requests?deviceId=cashdesk-1&requestId=clock-sync-1", "", "", "")
	if lookup.Code != http.StatusOK || lookup.Body.String() != first.Body.String() || lookup.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("persisted sync target/result lookup: HTTP %d %s", lookup.Code, lookup.Body)
	}
	if !bytes.Equal(commands, []byte{0x02, 0x72}) {
		t.Fatalf("duplicate request or lookup contacted the device again: % X", commands)
	}
}

func TestSyncDateTimeAPIRequiresRequestIDAndEmptyParams(t *testing.T) {
	a := testAPI(t, "550101046182", func(context.Context, byte, []byte) ([]byte, error) {
		t.Fatal("invalid synchronization request contacted the device")
		return nil, nil
	}, nil)
	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing request ID", `{"deviceId":"cashdesk-1","cmd":"syncDateTime","params":{}}`},
		{"caller dateTime", `{"deviceId":"cashdesk-1","cmd":"syncDateTime","requestId":"bad-sync","params":{"dateTime":"2026-11-01T12:00"}}`},
		{"caller timezone", `{"deviceId":"cashdesk-1","cmd":"syncDateTime","requestId":"bad-sync","params":{"timezone":"UTC"}}`},
		{"null params", `{"deviceId":"cashdesk-1","cmd":"syncDateTime","requestId":"bad-sync","params":null}`},
		{"array params", `{"deviceId":"cashdesk-1","cmd":"syncDateTime","requestId":"bad-sync","params":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := request(t, a, http.MethodPost, "/api/v1/commands", tc.body, "application/json", "")
			requireAPIError(t, w, http.StatusBadRequest, "invalid_request")
		})
	}
}

func TestSyncDateTimeAPICatalogueDescribesApplicationMutation(t *testing.T) {
	a := testAPI(t, "", func(context.Context, byte, []byte) ([]byte, error) {
		t.Fatal("command discovery contacted the device")
		return nil, nil
	}, nil)
	w := request(t, a, http.MethodGet, "/api/v1/commands", "", "", "")
	var catalogue struct {
		Commands []service.CommandDefinition `json:"commands"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &catalogue); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("catalogue request: HTTP %d %s", w.Code, w.Body)
	}
	for _, command := range catalogue.Commands {
		if command.Name != "syncDateTime" {
			continue
		}
		if command.ProtocolCode != "" || command.ReadOnly || !command.IdempotencyRequired || !command.Implemented || command.OperationRequired {
			t.Fatalf("syncDateTime must be an implemented application mutation: %+v", command)
		}
		if command.RequestLayout != "application" || command.ResponseLayout != "application" || len(command.Input) != 0 {
			t.Fatalf("syncDateTime application schemas: %+v", command)
		}
		if len(command.Output) != 1 || command.Output[0].Name != "dateTime" || command.Output[0].JSONType != "string" || !command.Output[0].Required {
			t.Fatalf("syncDateTime dateTime output schema: %+v", command.Output)
		}
		return
	}
	t.Fatal("syncDateTime was missing from the public command catalogue")
}

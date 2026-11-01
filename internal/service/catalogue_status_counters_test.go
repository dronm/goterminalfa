package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	terminalfa "github.com/dronm/goterminalfa"
	"github.com/dronm/goterminalfa/internal/protocol"
	"github.com/dronm/goterminalfa/internal/store"
)

type statusCounterExchange func(context.Context, byte, []byte) ([]byte, error)

func (f statusCounterExchange) Exchange(ctx context.Context, code byte, data []byte) ([]byte, error) {
	return f(ctx, code, data)
}

func statusCounterCommand(t *testing.T, code byte) Command {
	t.Helper()
	for _, command := range statusCounterCommands() {
		if command.Definition.ProtocolCode == fmt.Sprintf("0x%02X", code) {
			return command
		}
	}
	t.Fatalf("missing status/counter command 0x%02X", code)
	return Command{}
}

func statusCounterExecute(t *testing.T, code byte, params string, response []byte) (any, []byte, error) {
	t.Helper()
	command := statusCounterCommand(t, code)
	if err := command.Validate(json.RawMessage(params)); err != nil {
		return nil, nil, err
	}
	var sent []byte
	device := &Device{Client: terminalfa.NewClient(statusCounterExchange(func(_ context.Context, actual byte, payload []byte) ([]byte, error) {
		if actual != code {
			t.Errorf("sent command 0x%02X, wanted 0x%02X", actual, code)
		}
		sent = append([]byte{}, payload...)
		return response, nil
	}))}
	result, err := command.Execute(context.Background(), device, json.RawMessage(params))
	return result, sent, err
}

func statusCounterObject(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func statusCounterStatusResponse() []byte {
	data := []byte("550101046182")
	return append(data, 26, 11, 1, 15, 4, 0, 2, 1, 3)
}

func TestStatusCounterCatalogueCoverageAndPolicy(t *testing.T) {
	if got := len(statusCounterCommands()); got != 7 {
		t.Fatalf("got %d commands, want 7", got)
	}
	readOnly := map[byte]bool{0x01: true, 0xBB: true, 0xB0: true}
	for _, code := range []byte{0x01, 0xBB, 0xB0, 0xB1, 0xB2, 0xB3, 0xB4} {
		cmd := statusCounterCommand(t, code)
		if !cmd.Definition.Implemented || cmd.Definition.ReadOnly != readOnly[code] || cmd.Definition.IdempotencyRequired == readOnly[code] {
			t.Errorf("incorrect execution policy for 0x%02X: %+v", code, cmd.Definition)
		}
		if cmd.Definition.TitleRU == "" || cmd.Definition.DescriptionRU == "" || cmd.Definition.Source.Page == 0 {
			t.Errorf("missing documentation for 0x%02X", code)
		}
	}
	for _, code := range []byte{0xB3, 0xB4} {
		field := statusCounterCommand(t, code).Definition.Input[0]
		if field.Unit != "kopecks" || field.WireType != "VLN" || !field.Required {
			t.Fatalf("0x%02X lost exact monetary input metadata: %+v", code, field)
		}
	}
}

func TestStatusCounterGoldenRequestFrames(t *testing.T) {
	// Fixed reference frames were computed independently of the Go encoder from
	// the documented CCITT polynomial/init value and wire command bytes.
	cases := []struct {
		code     byte
		params   string
		response []byte
		frameHex string
	}{
		{0x01, `{}`, statusCounterStatusResponse(), "b6290001018cef"},
		{0xBB, `{}`, []byte{32}, "b6290001bb1de9"},
		{0xB0, `{}`, make([]byte, 10), "b6290001b07658"},
		{0xB1, `{}`, nil, "b6290001b15748"},
		{0xB2, `{}`, nil, "b6290001b23478"},
		{0xB3, `{"amountKopecks":12345}`, nil, "b6290003b3393021a4"},
		{0xB4, `{"amountKopecks":404969}`, nil, "b6290004b4e92d06ea92"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("0x%02X", tc.code), func(t *testing.T) {
			_, payload, err := statusCounterExecute(t, tc.code, tc.params, tc.response)
			if err != nil {
				t.Fatal(err)
			}
			frame, err := protocol.EncodeCommand(tc.code, payload)
			if err != nil {
				t.Fatal(err)
			}
			if got := hex.EncodeToString(frame); got != tc.frameHex {
				t.Fatalf("wire frame = %s, want %s", got, tc.frameHex)
			}
		})
	}
}

func TestKKTStatusDecodesEveryDocumentedField(t *testing.T) {
	result, sent, err := statusCounterExecute(t, 0x01, `{}`, statusCounterStatusResponse())
	if err != nil || len(sent) != 0 {
		t.Fatalf("status %#v, sent % X, error %v", result, sent, err)
	}
	want := map[string]any{
		"serialNumber": "550101046182", "dateTime": "2026-11-01T15:04",
		"criticalErrors": float64(0), "printerStatus": float64(2),
		"fiscalStoragePresent": float64(1), "lifecycleFlags": float64(3),
	}
	if got := statusCounterObject(t, result); !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded status = %#v, want %#v", got, want)
	}
	data := statusCounterStatusResponse()
	copy(data[17:], []byte{9, 255, 3, 128})
	result, _, err = statusCounterExecute(t, 0x01, `{}`, data)
	if err != nil {
		t.Fatal(err)
	}
	got := statusCounterObject(t, result)
	if got["criticalErrors"] != float64(9) || got["printerStatus"] != float64(255) || got["fiscalStoragePresent"] != float64(3) || got["lifecycleFlags"] != float64(128) {
		t.Fatalf("unknown firmware status values were lost: %+v", got)
	}
}

func TestPrintLineWidthRetainsFullByteRange(t *testing.T) {
	for _, value := range []byte{0, 32, 48, 255} {
		result, sent, err := statusCounterExecute(t, 0xBB, `{}`, []byte{value})
		if err != nil || len(sent) != 0 || statusCounterObject(t, result)["charactersPerLine"] != float64(value) {
			t.Fatalf("line width %d: %#v, sent % X, error %v", value, result, sent, err)
		}
	}
}

func TestKKTStatusChecksConfiguredPhysicalIdentity(t *testing.T) {
	command := statusCounterCommand(t, 0x01)
	device := &Device{ExpectedSerialNumber: "550101046183", Client: terminalfa.NewClient(statusCounterExchange(func(context.Context, byte, []byte) ([]byte, error) {
		return statusCounterStatusResponse(), nil
	}))}
	if _, err := command.Execute(context.Background(), device, json.RawMessage(`{}`)); err == nil {
		t.Fatal("status accepted a different physical KKT")
	} else {
		var identity *IdentityError
		if !errors.As(err, &identity) || identity.Actual != "550101046182" || identity.Expected != device.ExpectedSerialNumber {
			t.Fatalf("expected typed identity mismatch, got %v", err)
		}
	}
	device.ExpectedSerialNumber = "550101046182"
	if _, err := command.Execute(context.Background(), device, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("matching KKT identity rejected: %v", err)
	}
}

func TestCashCountersDecodeExactFiveByteIntegers(t *testing.T) {
	cases := []struct {
		wire []byte
		want uint64
	}{
		{[]byte{0, 0, 0, 0, 0}, 0},
		{[]byte{0xFF, 0xFF, 0xFF, 0xFF, 0}, 4294967295},
		{[]byte{0, 0, 0, 0, 1}, 4294967296},
		{[]byte{1, 2, 3, 4, 5}, 21542142465},
		{[]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, 1099511627775},
	}
	for _, tc := range cases {
		// Use asymmetric amounts to detect an accidental eight-byte width or
		// reversed interpretation of the two consecutive UINT40 fields.
		response := append(append([]byte{}, tc.wire...), 5, 4, 3, 2, 1)
		result, sent, err := statusCounterExecute(t, 0xB0, `{}`, response)
		if err != nil || len(sent) != 0 {
			t.Fatalf("counter response % X: %#v, sent % X, error %v", response, result, sent, err)
		}
		got := statusCounterObject(t, result)
		if got["cashKopecks"] != float64(tc.want) || got["electronicKopecks"] != float64(4328719365) {
			t.Fatalf("decoded counters = %#v", got)
		}
	}
}

func TestCashMovementVLNIsExactMinimalAndAllowsZero(t *testing.T) {
	cases := []struct {
		amount string
		want   []byte
	}{
		{"0", []byte{0}},
		{"255", []byte{255}},
		{"256", []byte{0, 1}},
		{"404969", []byte{0xE9, 0x2D, 0x06}},
		{"1099511627775", bytes.Repeat([]byte{0xFF}, 5)},
		{"1099511627776", []byte{0, 0, 0, 0, 0, 1}},
		{"18446744073709551615", bytes.Repeat([]byte{0xFF}, 8)},
	}
	for _, code := range []byte{0xB3, 0xB4} {
		for _, tc := range cases {
			for _, value := range []string{tc.amount, `"` + tc.amount + `"`} {
				params := `{"amountKopecks":` + value + `}`
				result, sent, err := statusCounterExecute(t, code, params, nil)
				if err != nil || !bytes.Equal(sent, tc.want) || len(statusCounterObject(t, result)) != 0 {
					t.Fatalf("0x%02X %s: sent % X, want % X, result %#v, error %v", code, params, sent, tc.want, result, err)
				}
			}
		}
	}
}

func TestStatusCounterRejectsInvalidParameters(t *testing.T) {
	for _, code := range []byte{0x01, 0xBB, 0xB0, 0xB1, 0xB2} {
		for _, params := range []string{`{"unexpected":1}`, `null`, `[]`} {
			if err := statusCounterCommand(t, code).Validate(json.RawMessage(params)); err == nil {
				t.Errorf("0x%02X accepted %s", code, params)
			}
		}
	}
	for _, code := range []byte{0xB3, 0xB4} {
		for _, params := range []string{
			`{}`, `null`, `[]`, `{"amountKopecks":null}`, `{"amountKopecks":true}`,
			`{"amountKopecks":-1}`, `{"amountKopecks":1.5}`, `{"amountKopecks":1e3}`,
			`{"amountKopecks":"1.5"}`, `{"amountKopecks":"-1"}`, `{"amountKopecks":""}`,
			`{"amountKopecks":18446744073709551616}`, `{"amountKopecks":"18446744073709551616"}`,
			`{"amountKopecks":1,"unexpected":2}`,
		} {
			if err := statusCounterCommand(t, code).Validate(json.RawMessage(params)); err == nil {
				t.Errorf("0x%02X accepted invalid amount parameters %s", code, params)
			}
		}
	}
}

func TestStatusCounterRejectsMalformedResponses(t *testing.T) {
	status := statusCounterStatusResponse()
	invalidDate := append([]byte{}, status...)
	invalidDate[13], invalidDate[14] = 2, 30
	cases := []struct {
		code byte
		data []byte
	}{
		{0x01, status[:20]}, {0x01, append(append([]byte{}, status...), 0)}, {0x01, invalidDate},
		{0xBB, nil}, {0xBB, []byte{32, 0}},
		{0xB0, make([]byte, 9)}, {0xB0, make([]byte, 11)},
		{0xB1, []byte{0}}, {0xB2, []byte{0}}, {0xB3, []byte{0}}, {0xB4, []byte{0}},
	}
	for _, tc := range cases {
		params := `{}`
		if tc.code == 0xB3 || tc.code == 0xB4 {
			params = `{"amountKopecks":1}`
		}
		if _, _, err := statusCounterExecute(t, tc.code, params, tc.data); err == nil {
			t.Errorf("0x%02X accepted malformed response % X", tc.code, tc.data)
		}
	}
}

func TestCashMovementUsesPersistedIdempotencyAndReadCountersBypassIt(t *testing.T) {
	var calls atomic.Int32
	device := testDevice("cash-counter-test", func(_ context.Context, code byte, payload []byte) ([]byte, error) {
		calls.Add(1)
		switch code {
		case 0xB3:
			if !bytes.Equal(payload, []byte{0x39, 0x30}) {
				t.Errorf("cash deposit bytes % X", payload)
			}
			return nil, nil
		case 0xB0:
			return make([]byte, 10), nil
		default:
			t.Errorf("unexpected wire command 0x%02X", code)
			return nil, nil
		}
	}, 1)
	app, journal := testService(t, []*Device{device}, statusCounterCommands())
	req := Request{DeviceID: device.ID, Command: "depositCash", Params: json.RawMessage(`{"amountKopecks":12345}`)}
	requireError(t, app.Execute(context.Background(), req), 400, "invalid_request")
	if calls.Load() != 0 {
		t.Fatal("request without an idempotency key reached the device")
	}
	req.RequestID = "deposit-1"
	first := app.Execute(context.Background(), req)
	second := app.Execute(context.Background(), req)
	if first.HTTPStatus != 200 || second.HTTPStatus != 200 || !second.Replayed || !bytes.Equal(first.Body, second.Body) || calls.Load() != 1 {
		t.Fatalf("deposit did not replay exactly: first %+v, second %+v, calls %d", first, second, calls.Load())
	}
	record, err := journal.Get(context.Background(), device.ID, req.RequestID)
	if err != nil || record.State != store.StateCompleted || record.Command != "depositCash" {
		t.Fatalf("deposit persistence = %+v, error %v", record, err)
	}
	for i := 0; i < 2; i++ {
		reply := app.Execute(context.Background(), Request{DeviceID: device.ID, Command: "getCashCounters", RequestID: "counter-read"})
		if reply.HTTPStatus != 200 || reply.Replayed {
			t.Fatalf("counter read was not fresh: %+v", reply)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("wire calls = %d, want 3", calls.Load())
	}
	if _, err := journal.Get(context.Background(), device.ID, "counter-read"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("read-only counter request was persisted: %v", err)
	}
}

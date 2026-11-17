package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/dronm/goterminalfa/internal/protocol"
	"github.com/dronm/goterminalfa/internal/store"
	"github.com/dronm/goterminalfa/internal/transport"
)

const ffd12ActivationParams = `{"activationCodeHex":"00112233445566778899aabbccddeeff"}`

func TestFFD12ActivationWirePayloadAndEmptyResponse(t *testing.T) {
	want := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}
	for _, response := range [][]byte{nil, {}, {0x00}} {
		device := testDevice("activation", func(_ context.Context, code byte, payload []byte) ([]byte, error) {
			if code != 0xDD || !bytes.Equal(payload, want) {
				t.Fatalf("activation code must be sixteen raw bytes: command=%02X payload=% X", code, payload)
			}
			return response, nil
		}, 1)
		result, err := ffd12ActivationCommand().Execute(context.Background(), device, json.RawMessage(ffd12ActivationParams))
		if len(response) != 0 {
			if !errors.Is(err, protocol.ErrMalformedFrame) {
				t.Fatalf("accepted unexpected activation response data: result=%v error=%v", result, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(result)
		if err != nil || string(encoded) != "{}" {
			t.Fatalf("empty protocol response must produce data {}: %s error=%v", encoded, err)
		}
	}
}

func TestFFD12ActivationFailureOutcomesAreJournaledWithoutResending(t *testing.T) {
	for _, test := range []struct {
		name     string
		response []byte
		err      error
		status   int
		code     string
		state    string
	}{
		{"device rejection", nil, &protocol.DeviceError{Code: 0x11}, 502, "device_error", store.StateCompleted},
		{"lost response", nil, io.ErrUnexpectedEOF, 409, "outcome_unknown", store.StateUnknown},
		{"unexpected response data", []byte{0x00}, nil, 409, "outcome_unknown", store.StateUnknown},
		{"not sent", nil, &transport.NotSentError{Err: io.ErrClosedPipe}, 502, "transport_error", store.StateCompleted},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			device := testDevice("activation", func(_ context.Context, code byte, _ []byte) ([]byte, error) {
				calls++
				if code != 0xDD {
					t.Fatalf("unexpected command %02X", code)
				}
				return test.response, test.err
			}, 1)
			device.ProtocolProfile = "ffd1.2"
			app, journal := testService(t, []*Device{device}, BuiltinCommands())
			req := Request{DeviceID: device.ID, Command: "applyActivationCode", RequestID: "activation-failure", Params: json.RawMessage(ffd12ActivationParams)}
			first := app.Execute(context.Background(), req)
			requireError(t, first, test.status, test.code)
			record, err := journal.Get(context.Background(), device.ID, req.RequestID)
			if err != nil || record.State != test.state {
				t.Fatalf("unexpected journal state %q error=%v", record.State, err)
			}
			req.Command = "0xdd"
			req.Params = json.RawMessage(`{"activationCodeHex":"00112233445566778899AABBCCDDEEFF"}`)
			replay := app.Execute(context.Background(), req)
			if replay.HTTPStatus != first.HTTPStatus || !bytes.Equal(replay.Body, first.Body) || calls != 1 || test.state == store.StateCompleted && !replay.Replayed {
				t.Fatalf("failed or uncertain activation was resent: first=%s replay=%s calls=%d", first.Body, replay.Body, calls)
			}
		})
	}
}

func TestFFD12ActivationOperationValidationAndCanonicalization(t *testing.T) {
	commands, err := BuiltinCommandsForProfile("ffd1.2")
	if err != nil {
		t.Fatal(err)
	}
	var operation Command
	for _, command := range commands {
		if command.Definition.Name == "executeOperation" {
			operation = command
		}
	}
	upper := json.RawMessage(`{"commands":[{"cmd":"0xDD","params":{"activationCodeHex":"00112233445566778899AABBCCDDEEFF"}}]}`)
	lower := json.RawMessage(`{"commands":[{"cmd":"applyActivationCode","params":{"activationCodeHex":"00112233445566778899aabbccddeeff"}}]}`)
	first, err := operation.Canonicalize(upper)
	if err != nil {
		t.Fatal(err)
	}
	second, err := operation.Canonicalize(lower)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("operation activation aliases or hex case are not canonical: first=%s second=%s error=%v", first, second, err)
	}
	insideReceipt := json.RawMessage(`{"commands":[{"cmd":"beginReceipt"},{"cmd":"applyActivationCode","params":{"activationCodeHex":"00112233445566778899aabbccddeeff"}},{"cmd":"cancelDocument"}]}`)
	if err := operation.Validate(insideReceipt); err == nil {
		t.Fatal("accepted activation while a fiscal document is unfinished")
	}
}

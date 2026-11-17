package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/dronm/goterminalfa/internal/protocol"
	"github.com/dronm/goterminalfa/internal/store"
)

func correctionReceiptTestParams() map[string]any {
	params := receiptTestParams()
	params["additionalData"] = map[string]any{"correctedCalculationDate": 1704067200}
	return params
}

func correctionReceiptTestRequest(t *testing.T, params map[string]any) Request {
	t.Helper()
	request := receiptTestRequest(t, params)
	request.Command = "createCorrectionReceipt"
	request.RequestID = "correction-1"
	return request
}

func correctionReceiptTestResponse(code, state byte) []byte {
	if code == 0x26 {
		return receiptTestResponse(0x24, state)
	}
	return receiptTestResponse(code, state)
}

func correctionReceiptTestTransport(state byte) *receiptTestTransport {
	transport := &receiptTestTransport{state: state}
	transport.respond = func(_ context.Context, call receiptWireCall) ([]byte, error) {
		return correctionReceiptTestResponse(call.Code, state), nil
	}
	return transport
}

func TestCreateCorrectionReceiptShiftBranchesFiscalDataAndRestartReplay(t *testing.T) {
	for _, state := range []byte{0, 1} {
		for sign := 1; sign <= 4; sign++ {
			t.Run(fmt.Sprintf("shift=%d/sign=%d", state, sign), func(t *testing.T) {
				transport := correctionReceiptTestTransport(state)
				app, device, journal := receiptTestApp(t, transport, "550101046182")
				params := correctionReceiptTestParams()
				params["calculationSign"] = sign
				params["suppressShiftPrint"] = 1
				params["printSuppressionCode"] = 41029
				params["additionalData"].(map[string]any)["prescriptionNumber"] = "N-1"
				request := correctionReceiptTestRequest(t, params)
				first := app.Execute(context.Background(), request)
				if first.HTTPStatus != 200 || responseBody(t, first).Command != "createCorrectionReceipt" {
					t.Fatalf("correction creation failed: %s", first.Body)
				}
				want := []byte{0x02, 0x20, 0x25, 0x96, 0x98, 0x2D, 0x26}
				if state == 0 {
					want = []byte{0x02, 0x20, 0x21, 0x2F, 0x22, 0x25, 0x96, 0x98, 0x2D, 0x26}
				}
				if got := receiptCalls(transport); !bytes.Equal(got, want) {
					t.Fatalf("correction wire commands % X, want % X", got, want)
				}
				data := receiptReplyData(t, first)
				if data["shiftOpenedByRequest"] != (state == 0) || data["receipt"] == nil {
					t.Fatalf("missing correction/shift result: %s", first.Body)
				}
				_, hasShift := data["shiftResult"]
				if hasShift != (state == 0) {
					t.Fatalf("wrong conditional shift result: %s", first.Body)
				}
				for _, call := range transport.calls {
					if call.Session != 1 {
						t.Fatalf("correction primary operation changed session: %+v", call)
					}
					switch call.Code {
					case 0x21:
						if !bytes.Equal(call.Payload, []byte{1}) {
							t.Fatalf("wrong correction shift printing flag: % X", call.Payload)
						}
					case 0x25:
						if !bytes.Equal(call.Payload, []byte{0x45, 0xA0}) {
							t.Fatalf("correction printing flag was not passed to 0x25: % X", call.Payload)
						}
					case 0x98:
						if !bytes.Equal(call.Payload, []byte{0x9A, 0x04, 4, 0, 0x80, 0, 0x92, 0x65, 0x9B, 0x04, 3, 0, 'N', '-', '1'}) {
							t.Fatalf("correction date/prescription must be direct tags 1178/1179: % X", call.Payload)
						}
					case 0x26:
						if !bytes.Equal(call.Payload, []byte{byte(sign), 100, 0, 0, 0, 0}) {
							t.Fatalf("correction finalizer must use selected sign plus UINT40 total: % X", call.Payload)
						}
					}
				}
				restarted := serviceWithJournal(t, []*Device{device}, BuiltinCommands(), journal)
				replay := restarted.Execute(context.Background(), request)
				if !replay.Replayed || !bytes.Equal(first.Body, replay.Body) || !bytes.Equal(receiptCalls(transport), want) || transport.opens != 1 || transport.closes != 1 {
					t.Fatalf("correction was executed again after service restart: %s", replay.Body)
				}
			})
		}
	}
}

func TestCreateCorrectionReceiptRejectsInvalidCorrectionDataBeforeIO(t *testing.T) {
	cases := map[string]func(map[string]any){
		"missing additional data": func(params map[string]any) { delete(params, "additionalData") },
		"null additional data":    func(params map[string]any) { params["additionalData"] = nil },
		"array additional data":   func(params map[string]any) { params["additionalData"] = []any{} },
		"string additional data":  func(params map[string]any) { params["additionalData"] = "not an object" },
		"missing correction date": func(params map[string]any) { params["additionalData"] = map[string]any{} },
		"prescription without date": func(params map[string]any) {
			params["additionalData"] = map[string]any{"prescriptionNumber": "N-1"}
		},
		"null correction date": func(params map[string]any) {
			params["additionalData"].(map[string]any)["correctedCalculationDate"] = nil
		},
		"negative correction date": func(params map[string]any) {
			params["additionalData"].(map[string]any)["correctedCalculationDate"] = -1
		},
		"overflow correction date": func(params map[string]any) {
			params["additionalData"].(map[string]any)["correctedCalculationDate"] = uint64(4294967296)
		},
		"fractional correction date": func(params map[string]any) {
			params["additionalData"].(map[string]any)["correctedCalculationDate"] = 1.5
		},
		"string correction date": func(params map[string]any) {
			params["additionalData"].(map[string]any)["correctedCalculationDate"] = "1704067200"
		},
		"invalid prescription type": func(params map[string]any) {
			params["additionalData"].(map[string]any)["prescriptionNumber"] = true
		},
		"oversized prescription": func(params map[string]any) {
			params["additionalData"].(map[string]any)["prescriptionNumber"] = strings.Repeat("Я", 33)
		},
		"legacy correction wrapper": func(params map[string]any) {
			params["additionalData"] = map[string]any{"basis": map[string]any{"date": 1704067200}}
		},
		"malformed nested industry data": func(params map[string]any) {
			params["additionalData"].(map[string]any)["industryAttributes"] = []any{map[string]any{"authorityCode": 1}}
		},
		"cashier required on open shift": func(params map[string]any) { delete(params, "cashierName") },
		"empty items":                    func(params map[string]any) { params["items"] = []any{} },
		"invalid printing flag":          func(params map[string]any) { params["printSuppressionCode"] = 1 },
		"invalid correction sign":        func(params map[string]any) { params["calculationSign"] = 5 },
		"too many items": func(params map[string]any) {
			items := make([]any, 201)
			for index := range items {
				items[index] = params["items"].([]any)[0]
			}
			params["items"] = items
		},
	}
	for name, modify := range cases {
		t.Run(name, func(t *testing.T) {
			transport := correctionReceiptTestTransport(1)
			app, _, journal := receiptTestApp(t, transport, "550101046182")
			params := correctionReceiptTestParams()
			modify(params)
			request := correctionReceiptTestRequest(t, params)
			requireError(t, app.Execute(context.Background(), request), 400, "invalid_request")
			if transport.opens != 0 || len(receiptCalls(transport)) != 0 {
				t.Fatal("invalid correction data reached identity verification or device I/O")
			}
			if _, err := journal.Get(context.Background(), request.DeviceID, request.RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("invalid correction consumed its request ID: %v", err)
			}
		})
	}
	transport := correctionReceiptTestTransport(1)
	app, _, _ := receiptTestApp(t, transport, "")
	request := correctionReceiptTestRequest(t, correctionReceiptTestParams())
	request.RequestID = ""
	requireError(t, app.Execute(context.Background(), request), 400, "invalid_request")
	if transport.opens != 0 {
		t.Fatal("correction without a request ID reached the device")
	}
}

func TestCreateCorrectionReceiptKeepsRequiredDateWhenPaymentIsOmitted(t *testing.T) {
	transport := correctionReceiptTestTransport(1)
	app, _, _ := receiptTestApp(t, transport, "")
	params := correctionReceiptTestParams()
	delete(params, "payment")
	params["additionalData"] = map[string]any{"correctedCalculationDate": 0}
	reply := app.Execute(context.Background(), correctionReceiptTestRequest(t, params))
	if reply.HTTPStatus != 200 || !bytes.Equal(receiptCalls(transport), []byte{0x02, 0x20, 0x25, 0x96, 0x98, 0x26}) {
		t.Fatalf("omitted payment changed the correction sequence: calls=% X body=%s", receiptCalls(transport), reply.Body)
	}
	for _, call := range transport.calls {
		if call.Code == 0x25 && len(call.Payload) != 0 {
			t.Fatalf("omitted correction printing flag was fabricated: % X", call.Payload)
		}
		if call.Code == 0x98 && !bytes.Equal(call.Payload, []byte{0x9A, 0x04, 4, 0, 0, 0, 0, 0}) {
			t.Fatalf("explicit zero correction date was omitted or changed: % X", call.Payload)
		}
	}
}

func TestCreateCorrectionReceiptCleanupOwnershipAndAmbiguousFinalization(t *testing.T) {
	for _, test := range []struct {
		name       string
		state      byte
		failCode   byte
		failure    error
		wantStatus int
		wantCode   string
		wantCancel bool
	}{
		{"item rejected", 1, 0x96, &protocol.DeviceError{Code: 0x28}, 502, "device_error", true},
		{"additional data rejected after shift opening", 0, 0x98, &protocol.DeviceError{Code: 0x02}, 502, "device_error", true},
		{"correction finalizer rejected", 1, 0x26, &protocol.DeviceError{Code: 0x02}, 502, "device_error", true},
		{"correction finalizer response lost", 1, 0x26, io.ErrUnexpectedEOF, 409, "outcome_unknown", true},
		{"correction begin rejected", 1, 0x25, &protocol.DeviceError{Code: 0x02}, 502, "device_error", false},
		{"correction begin response lost after shift opening", 0, 0x25, io.ErrUnexpectedEOF, 409, "outcome_unknown", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := correctionReceiptTestTransport(test.state)
			transport.respond = func(ctx context.Context, call receiptWireCall) ([]byte, error) {
				if call.Code == test.failCode {
					return nil, test.failure
				}
				if call.Code == 0x10 && ctx.Err() != nil {
					t.Fatal("correction cleanup inherited a failed primary context")
				}
				return correctionReceiptTestResponse(call.Code, test.state), nil
			}
			app, _, journal := receiptTestApp(t, transport, "")
			request := correctionReceiptTestRequest(t, correctionReceiptTestParams())
			first := app.Execute(context.Background(), request)
			requireError(t, first, test.wantStatus, test.wantCode)
			cleanup := requireReceiptCleanup(t, first, test.wantCancel, test.wantCancel)
			data := receiptReplyData(t, first)
			if data["failedCommand"] != fmt.Sprintf("0x%02X", test.failCode) || data["shiftOpenedByRequest"] != (test.state == 0) {
				t.Fatalf("correction failure lost stage/shift details: %s", first.Body)
			}
			if test.failCode == 0x26 && data["failedStage"] != "finalizeCorrectionReceipt" {
				t.Fatalf("correction failure reported the normal receipt finalizer: %s", first.Body)
			}
			codes := receiptCalls(transport)
			if bytes.Contains(codes, []byte{0x23}) || bytes.Contains(codes, []byte{0x24}) || bytes.Contains(codes, []byte{0x10}) != test.wantCancel {
				t.Fatalf("wrong correction cancellation commands: % X", codes)
			}
			if test.wantCancel {
				if transport.opens != 2 || transport.closes != 2 || !strings.Contains(strings.Join(transport.events, ","), "close:1,open:2") {
					t.Fatalf("correction cleanup did not use a fresh session: %v", transport.events)
				}
				last := transport.calls[len(transport.calls)-2:]
				if last[0].Code != 0x02 || last[1].Code != 0x10 || last[0].Session != 2 || last[1].Session != 2 {
					t.Fatalf("correction cleanup did not verify identity before cancellation: %+v", last)
				}
			} else if cleanup["skippedReason"] == nil || transport.opens != 1 {
				t.Fatalf("unconfirmed correction begin caused cleanup: %s events=%v", first.Body, transport.events)
			}
			record, err := journal.Get(context.Background(), request.DeviceID, request.RequestID)
			if err != nil || !bytes.Equal(record.Response, first.Body) || (record.State == store.StateUnknown) != (test.wantStatus == 409) {
				t.Fatalf("wrong persisted correction outcome: %+v error=%v", record, err)
			}
			retry := app.Execute(context.Background(), request)
			if !bytes.Equal(first.Body, retry.Body) || !bytes.Equal(receiptCalls(transport), codes) {
				t.Fatalf("failed or ambiguous correction executed again: %s", retry.Body)
			}
			if test.failCode == 0x26 && test.wantStatus == 409 && cleanup["succeeded"] != true {
				t.Fatal("lost correction finalization must stay unknown even after acknowledged cancellation")
			}
		})
	}
}

func TestCreateCorrectionReceiptCleanupRefusesAnotherPhysicalDevice(t *testing.T) {
	transport := correctionReceiptTestTransport(1)
	transport.respond = func(_ context.Context, call receiptWireCall) ([]byte, error) {
		if call.Code == 0x98 {
			return nil, &protocol.DeviceError{Code: 0x28}
		}
		if call.Code == 0x02 && call.Session == 2 {
			return []byte("550101046183"), nil
		}
		return correctionReceiptTestResponse(call.Code, 1), nil
	}
	app, _, _ := receiptTestApp(t, transport, "")
	reply := app.Execute(context.Background(), correctionReceiptTestRequest(t, correctionReceiptTestParams()))
	requireError(t, reply, 409, "outcome_unknown")
	cleanup := requireReceiptCleanup(t, reply, true, false)
	problem, ok := cleanup["error"].(map[string]any)
	if !ok || problem["code"] != "device_identity_mismatch" || !bytes.Equal(receiptCalls(transport), []byte{0x02, 0x20, 0x25, 0x96, 0x98, 0x02}) {
		t.Fatalf("correction cleanup did not refuse the different KKT: calls=% X body=%s", receiptCalls(transport), reply.Body)
	}
}

func TestCreateCorrectionReceiptIsFFDOnlyAndCannotBeNested(t *testing.T) {
	base := unusedDevice(t, "kkt-1")
	app, _ := testService(t, []*Device{base}, BuiltinCommands())
	request := correctionReceiptTestRequest(t, correctionReceiptTestParams())
	requireError(t, app.Execute(context.Background(), request), 404, "unknown_command")
	transport := correctionReceiptTestTransport(1)
	ffd, _, _ := receiptTestApp(t, transport, "")
	params, _ := json.Marshal(map[string]any{"commands": []any{map[string]any{"cmd": "createCorrectionReceipt", "params": correctionReceiptTestParams()}}})
	request.Command, request.Params = "executeOperation", params
	requireError(t, ffd.Execute(context.Background(), request), 400, "invalid_request")
	if transport.opens != 0 {
		t.Fatal("nested correction workflow reached the device")
	}
}

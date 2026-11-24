package service

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	terminalfa "github.com/dronm/goterminalfa"
)

func TestConcurrentCloseShiftIfOpenClosesOnlyOnce(t *testing.T) {
	const serial = "550101046182"
	checked, resume := make(chan struct{}), make(chan struct{})
	open := true
	transport := &operationSessionExchange{execute: func(ctx context.Context, code byte, _ []byte) ([]byte, error) {
		switch code {
		case 0x02:
			return []byte(serial), nil
		case 0x20:
			if open {
				close(checked)
				select {
				case <-resume:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return []byte{1, 42, 0, 0, 0}, nil
			}
			return []byte{0, 42, 0, 0, 0}, nil
		case 0x2A:
			open = false
			return []byte{42, 0, 1, 0, 0, 0, 2, 0, 0, 0}, nil
		}
		return nil, nil
	}}
	device := NewDevice("kkt-1", "serial", serial, terminalfa.NewClient(transport), 2)
	app, _ := testService(t, []*Device{device}, BuiltinCommands())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	closeShift := func(id string) <-chan Reply {
		done := make(chan Reply, 1)
		go func() {
			done <- app.Execute(ctx, Request{DeviceID: device.ID, Command: "closeShiftIfOpen", RequestID: id, Params: json.RawMessage(`{"cashierName":"A"}`)})
		}()
		return done
	}
	first := closeShift("admin")
	awaitSignal(t, checked)
	second := closeShift("cron")
	awaitSlots(t, device, 2)
	close(resume)
	for i, done := range []<-chan Reply{first, second} {
		reply := awaitReply(t, done)
		if reply.HTTPStatus != 200 {
			t.Fatalf("request %d: %s", i, reply.Body)
		}
		data := responseBody(t, reply).Data.(map[string]any)
		if data["alreadyClosed"] != (i == 1) || data["shiftNumber"] != float64(42) {
			t.Fatalf("request %d: unexpected data %s", i, reply.Body)
		}
	}
	want := []byte{0x02, 0x20, 0x29, 0x2F, 0x2A, 0x02, 0x20}
	if !bytes.Equal(transport.calls, want) || transport.opens != 2 || transport.closes != 2 {
		t.Fatalf("commands = % X, sessions opened/closed = %d/%d", transport.calls, transport.opens, transport.closes)
	}
}

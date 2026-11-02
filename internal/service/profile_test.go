package service

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestFFD12CataloguePreservesBaseAndRejectsAmbiguousAndSupersededCommands(t *testing.T) {
	before := BuiltinCommands()
	ffd, err := BuiltinCommandsForProfile("ffd1.2")
	if err != nil {
		t.Fatal(err)
	}
	if len(ffd) != 80 {
		t.Fatalf("FFD catalogue has %d entries", len(ffd))
	}
	if _, err := BuiltinCommandsForProfile("invalid"); err == nil {
		t.Fatal("accepted unsupported profile")
	}
	implemented := 0
	for _, cmd := range ffd {
		if cmd.Definition.ProtocolProfile != "ffd1.2" {
			t.Fatalf("missing profile: %s", cmd.Definition.Name)
		}
		if cmd.Definition.ProtocolCode != "" && cmd.Definition.Implemented {
			implemented++
		}
	}
	if implemented != 65 {
		t.Fatalf("implemented FFD wire commands %d, want 65", implemented)
	}
	after := BuiltinCommands()
	for i := range before {
		if !reflect.DeepEqual(before[i].Definition, after[i].Definition) {
			t.Fatalf("profile building mutated base definition %s", before[i].Definition.Name)
		}
	}
	device := unusedDevice(t, "ffd")
	device.ProtocolProfile = "ffd1.2"
	app, _ := testService(t, []*Device{device}, before)
	for _, code := range []string{"0x12", "0x13", "0x14", "0x15", "0x16", "0x17", "0x1F", "0x2B", "0x2C", "0x2E", "0x3F", "0x34"} {
		requireError(t, app.Execute(context.Background(), Request{DeviceID: device.ID, Command: code}), 501, "command_unavailable")
	}
	for _, cmd := range ffd {
		if cmd.Definition.ProtocolCode == "0x14" || cmd.Definition.ProtocolCode == "0x17" {
			if !strings.Contains(cmd.Definition.UnavailableReason, "0x28") || strings.Contains(cmd.Definition.UnavailableReason, "without a payload") {
				t.Fatalf("stale reason after supplement supplied payload: %+v", cmd.Definition)
			}
		}
	}
}

func TestProfileSelectionPreservesLegacyIdempotencyAndBlocksCrossProfileReplay(t *testing.T) {
	calls := 0
	device := testDevice("kkt-1", func(_ context.Context, code byte, _ []byte) ([]byte, error) {
		calls++
		if code != 0x72 {
			t.Fatalf("unexpected protocol command: %02X", code)
		}
		return nil, nil
	}, 2)
	app, journal := testService(t, []*Device{device}, BuiltinCommands())
	req := Request{DeviceID: device.ID, Command: "setDateTime", RequestID: "clock-before-profile", Params: json.RawMessage(`{"dateTime":"2026-11-02T12:34"}`)}
	first := app.Execute(context.Background(), req)
	if first.HTTPStatus != 200 || calls != 1 {
		t.Fatalf("base execution failed: %s", first.Body)
	}
	legacy := serviceWithJournal(t, []*Device{device}, BuiltinCommands(), journal)
	replay := legacy.Execute(context.Background(), req)
	if !replay.Replayed || !bytes.Equal(first.Body, replay.Body) || calls != 1 {
		t.Fatalf("legacy replay compatibility lost: %s", replay.Body)
	}
	ffd := testDevice(device.ID, func(_ context.Context, code byte, _ []byte) ([]byte, error) {
		calls++
		return nil, nil
	}, 2)
	ffd.ProtocolProfile = "ffd1.2"
	migrated := serviceWithJournal(t, []*Device{ffd}, BuiltinCommands(), journal)
	requireError(t, migrated.Execute(context.Background(), req), 409, "idempotency_conflict")
	if lookup := migrated.Lookup(context.Background(), device.ID, req.RequestID); !bytes.Equal(lookup.Body, first.Body) {
		t.Fatal("old result became inaccessible")
	}
	req.RequestID = "clock-after-profile"
	newResult := migrated.Execute(context.Background(), req)
	if newResult.HTTPStatus != 200 || calls != 2 {
		t.Fatalf("new-profile intent failed: %s", newResult.Body)
	}
	if replay := migrated.Execute(context.Background(), req); !replay.Replayed || !bytes.Equal(replay.Body, newResult.Body) || calls != 2 {
		t.Fatal("FFD request was not replayed")
	}
}

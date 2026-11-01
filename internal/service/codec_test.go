package service

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExactDecimalAndIntegerWireBoundaries(t *testing.T) {
	for _, tc := range []struct {
		kind, raw string
		bytes     []byte
	}{
		{"VLN", `0`, []byte{0}},
		{"VLN", `"18446744073709551615"`, []byte{255, 255, 255, 255, 255, 255, 255, 255}},
		{"UINT40", `1099511627775`, []byte{255, 255, 255, 255, 255}},
		{"FVLN", `"1234567.89"`, []byte{2, 0x15, 0xcd, 0x5b, 7}},
		{"FVLN", `"0.000"`, []byte{3, 0}},
	} {
		got, err := encodeValue(ParameterDefinition{WireType: tc.kind}, json.RawMessage(tc.raw))
		if err != nil || !bytes.Equal(got, tc.bytes) {
			t.Fatalf("%s %s -> % X, %v", tc.kind, tc.raw, got, err)
		}
	}
	for _, tc := range []struct{ kind, raw string }{
		{"BYTE", `256`}, {"BYTE", `"1"`}, {"UINT16", `-1`}, {"UINT32", `1.5`}, {"UINT40", `1099511627776`},
		{"VLN", `"18446744073709551616"`}, {"VLN", `1e2`}, {"FVLN", `1.25`}, {"FVLN", `"-1.2"`}, {"FVLN", `"1."`},
	} {
		if _, err := encodeValue(ParameterDefinition{WireType: tc.kind}, json.RawMessage(tc.raw)); err == nil {
			t.Fatalf("accepted %s %s", tc.kind, tc.raw)
		}
	}
}

func TestCP866EncodingAndByteLimits(t *testing.T) {
	f := ParameterDefinition{WireType: "ASCII", MaxLength: 2}
	got, err := encodeValue(f, json.RawMessage(`"Яя"`))
	if err != nil || !bytes.Equal(got, []byte{0x9f, 0xef}) {
		t.Fatalf("CP866 % X %v", got, err)
	}
	for _, raw := range []string{`"Яяя"`, `"😀"`} {
		if _, err := encodeValue(f, json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestCalendarValuesValidateWithoutInventingTimezone(t *testing.T) {
	f := ParameterDefinition{WireType: "DATETIME5", AllowZeroDate: true}
	got, err := encodeValue(f, json.RawMessage(`"2024-02-29T23:59"`))
	if err != nil || !bytes.Equal(got, []byte{24, 2, 29, 23, 59}) {
		t.Fatalf("date % X %v", got, err)
	}
	if value, err := decodeValue(f, []byte{0, 0, 0, 0, 0}); err != nil || value != nil {
		t.Fatalf("zero date %v %v", value, err)
	}
	for _, raw := range []string{`"2025-02-29T23:59"`, `"2024-01-01T24:00"`, `"2024-01-01T12:00Z"`, `"1999-01-01T12:00"`} {
		if _, err := encodeValue(f, json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, err := decodeValue(f, []byte{26, 2, 30, 12, 0}); err == nil {
		t.Fatal("accepted impossible response date")
	}
}

func TestTLVUnknownFieldsAndRepeatIntegrity(t *testing.T) {
	data := []byte{1, 0, 1, 0, 7, 99, 0, 2, 0, 0xab, 0xcd}
	fields := []ParameterDefinition{{Name: "value", WireType: "BYTE", Tag: 1, Required: true}}
	decoded, err := decodeFields(fields, "tlv", data)
	if err != nil || decoded["value"] != uint64(7) || decoded["unknownTags"] == nil {
		t.Fatalf("decode %v %v", decoded, err)
	}
	if _, err := decodeFields(fields, "tlv", append(data, []byte{1, 0, 1, 0, 8}...)); err == nil {
		t.Fatal("accepted duplicated scalar tag")
	}
	for _, bad := range [][]byte{{1}, {1, 0, 3, 0, 0}, {1, 0, 2}} {
		if _, err := ParseTLV(bad); err == nil {
			t.Fatalf("accepted % X", bad)
		}
	}
}

func TestFullCatalogueScopeAndUnresolvedDefinitions(t *testing.T) {
	expected := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0E, 0x10, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x19, 0x1F, 0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2A, 0x2B, 0x2C, 0x2D, 0x2E, 0x2F, 0x30, 0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x39, 0x3A, 0x3B, 0x3F, 0x40, 0x50, 0x61, 0x62, 0x63, 0x64, 0x65, 0x69, 0x72, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x80, 0x81, 0xB0, 0xB1, 0xB2, 0xB3, 0xB4, 0xBB}
	commands := BuiltinCommands()
	if len(commands) != 74 {
		t.Fatalf("got %d entries", len(commands))
	}
	var implemented, unavailable int
	for i, code := range expected {
		def := commands[i].Definition
		if value, ok := operationProtocolCode(def.ProtocolCode); !ok || value != code {
			t.Fatalf("position %d: %s", i, def.ProtocolCode)
		}
		if def.Implemented {
			implemented++
		} else {
			unavailable++
			if def.UnavailableReason == "" {
				t.Fatal("missing unavailability reason")
			}
		}
		if def.TitleRU == "" || def.DescriptionRU == "" || def.Source.Page == 0 {
			t.Fatalf("missing source documentation: %s", def.Name)
		}
	}
	if implemented != 68 || unavailable != 4 {
		t.Fatalf("implemented %d unavailable %d", implemented, unavailable)
	}
}

func FuzzTLVRoundTrip(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{1, 0, 1, 0, 7})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, data []byte) {
		items, err := ParseTLV(data)
		if err != nil {
			return
		}
		var rebuilt []byte
		for _, item := range items {
			rebuilt, err = AppendTLV(rebuilt, item.Tag, item.Value)
			if err != nil {
				t.Fatal(err)
			}
		}
		if !bytes.Equal(data, rebuilt) {
			t.Fatalf("changed TLV bytes")
		}
	})
}

func TestDocumentedExamplesValidateAgainstCatalogue(t *testing.T) {
	lookup := map[string]Command{}
	for _, cmd := range BuiltinCommands() {
		lookup[cmd.Definition.Name] = cmd
		for _, alias := range cmd.Definition.Aliases {
			lookup[alias] = cmd
		}
	}
	paths, err := filepath.Glob("../../examples/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no examples: %v", err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var request Request
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Fatal(err)
			}
			cmd, ok := lookup[request.Command]
			if !ok || !cmd.Definition.Implemented {
				t.Fatalf("unavailable example command: %s", request.Command)
			}
			params, err := canonicalParams(request.Params)
			if err != nil {
				t.Fatal(err)
			}
			if cmd.Canonicalize != nil {
				params, err = cmd.Canonicalize(params)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := cmd.Validate(params); err != nil {
				t.Fatalf("invalid documented example: %v", err)
			}
			if cmd.Definition.IdempotencyRequired && request.RequestID == "" {
				t.Fatal("mutating example lacks requestId")
			}
		})
	}
}

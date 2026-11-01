package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"testing"
)

func decodeHex(t *testing.T, value string) []byte {
	t.Helper()
	data, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func response(result byte, payload []byte) []byte {
	frame, _ := EncodeCommand(result, payload)
	return frame
}

func TestDocumentedCommandVector(t *testing.T) {
	got, err := EncodeCommand(0x30, []byte{1, 0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	want := decodeHex(t, "b6290005300100000095c8")
	if !bytes.Equal(got, want) {
		t.Fatalf("got % X, want documented vector % X", got, want)
	}
}

func TestObservedSerialNumberResponse(t *testing.T) {
	// Captured from the user's physical Terminal FA.
	frame := decodeHex(t, "b629000d0035353031303130343631383252de")
	data, err := ReadResponse(&oneByteReader{data: frame})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "550101046182" {
		t.Fatalf("serial number %q", data)
	}
	request, err := EncodeCommand(0x02, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(request, decodeHex(t, "b629000102efdf")) {
		t.Fatalf("query packet % X", request)
	}
}

func TestProtocolRejectsCorruptAndMalformedResponses(t *testing.T) {
	badCRC := response(0, []byte("serial"))
	badCRC[len(badCRC)-1] ^= 1
	cases := []struct {
		name  string
		frame []byte
		want  error
	}{
		{"bad CRC", badCRC, ErrChecksumMismatch},
		{"bad marker", []byte{0, 0, 0, 1}, ErrMalformedFrame},
		{"empty body", []byte{0xB6, 0x29, 0, 0}, ErrMalformedFrame},
		{"oversized body", []byte{0xB6, 0x29, 4, 1}, ErrMalformedFrame},
		{"unknown result", response(2, nil), ErrMalformedFrame},
		{"missing error code", response(1, nil), ErrMalformedFrame},
		{"extra error bytes", response(1, []byte{3, 4}), ErrMalformedFrame},
		{"truncated header", []byte{0xB6}, io.ErrUnexpectedEOF},
		{"truncated body", []byte{0xB6, 0x29, 0, 1, 0}, io.ErrUnexpectedEOF},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadResponse(bytes.NewReader(tc.frame))
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDeviceError(t *testing.T) {
	_, err := ReadResponse(bytes.NewReader(response(1, []byte{0x30})))
	var deviceError *DeviceError
	if !errors.As(err, &deviceError) || deviceError.Code != 0x30 {
		t.Fatalf("device error = %v", err)
	}
}

func TestPayloadBoundsAndBigEndianLength(t *testing.T) {
	if _, err := EncodeCommand(1, make([]byte, MaxPayloadSize+1)); !errors.Is(err, ErrMalformedFrame) {
		t.Fatalf("oversized payload error = %v", err)
	}
	frame, err := EncodeCommand(1, make([]byte, MaxPayloadSize))
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.BigEndian.Uint16(frame[2:4]); got != 1024 {
		t.Fatalf("body length = %d", got)
	}
	if _, err := ReadResponse(bytes.NewReader(response(0, make([]byte, MaxPayloadSize)))); err != nil {
		t.Fatal(err)
	}
}

type oneByteReader struct {
	data []byte
}

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	p[0] = r.data[0]
	r.data = r.data[1:]
	return 1, nil
}

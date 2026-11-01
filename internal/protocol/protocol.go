// Package protocol implements the Terminal FA binary command framing.
// The framing is independent of whether bytes travel over USB serial or TCP.
package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// MaxPayloadSize is the maximum DATA length specified by protocol 01.05.
	MaxPayloadSize = 1023
	maxBodySize    = MaxPayloadSize + 1
)

var (
	ErrMalformedFrame   = errors.New("malformed Terminal FA frame")
	ErrChecksumMismatch = errors.New("Terminal FA CRC mismatch")
)

// DeviceError is a rejection returned by the KKT (RESULT=1).
// Code is the device's error byte, not an HTTP status or transport error.
type DeviceError struct {
	Code byte
}

func (e *DeviceError) Error() string {
	return fmt.Sprintf("KKT rejected command with error 0x%02X", e.Code)
}

// CRC16 computes CRC16-CCITT with polynomial 0x1021 and initial value 0xFFFF.
// The start bytes and CRC bytes are not included in data.
func CRC16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, value := range data {
		crc ^= uint16(value) << 8
		for bit := 0; bit < 8; bit++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// EncodeCommand builds START + LENGTH(BE) + CMD + DATA + CRC(LE).
func EncodeCommand(command byte, payload []byte) ([]byte, error) {
	if len(payload) > MaxPayloadSize {
		return nil, fmt.Errorf("%w: payload exceeds %d bytes", ErrMalformedFrame, MaxPayloadSize)
	}
	frame := make([]byte, 7+len(payload))
	frame[0], frame[1] = 0xB6, 0x29
	binary.BigEndian.PutUint16(frame[2:4], uint16(1+len(payload)))
	frame[4] = command
	copy(frame[5:], payload)
	binary.LittleEndian.PutUint16(frame[len(frame)-2:], CRC16(frame[2:len(frame)-2]))
	return frame, nil
}

// ReadResponse reads exactly one complete response, validates it, and returns DATA.
// It never scans past malformed bytes or silently discards a response. The caller
// is responsible for deadlines and deciding how a broken exchange is recovered.
func ReadResponse(reader io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, fmt.Errorf("read response header: %w", err)
	}
	if header[0] != 0xB6 || header[1] != 0x29 {
		return nil, fmt.Errorf("%w: invalid start marker % X", ErrMalformedFrame, header[:2])
	}
	length := int(binary.BigEndian.Uint16(header[2:]))
	if length < 1 || length > maxBodySize {
		return nil, fmt.Errorf("%w: invalid body length %d", ErrMalformedFrame, length)
	}
	body := make([]byte, length+2)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	checked := make([]byte, 2+length)
	copy(checked, header[2:])
	copy(checked[2:], body[:length])
	actual := CRC16(checked)
	expected := binary.LittleEndian.Uint16(body[length:])
	if actual != expected {
		return nil, fmt.Errorf("%w: calculated %04X, received %04X", ErrChecksumMismatch, actual, expected)
	}
	switch body[0] {
	case 0:
		return body[1:length], nil
	case 1:
		if length != 2 {
			return nil, fmt.Errorf("%w: error result requires one error byte", ErrMalformedFrame)
		}
		return nil, &DeviceError{Code: body[1]}
	default:
		return nil, fmt.Errorf("%w: unknown result 0x%02X", ErrMalformedFrame, body[0])
	}
}

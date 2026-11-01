package service

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/charmap"
)

// TLV retains order, unknown tags, and repeated tags without loss.
type TLV struct {
	Tag   uint16
	Value []byte
}

func AppendTLV(dst []byte, tag uint16, value []byte) ([]byte, error) {
	if len(value) > 65535 {
		return nil, fmt.Errorf("tag %d value exceeds 65535 bytes", tag)
	}
	header := make([]byte, 4)
	binary.LittleEndian.PutUint16(header, tag)
	binary.LittleEndian.PutUint16(header[2:], uint16(len(value)))
	dst = append(dst, header...)
	return append(dst, value...), nil
}

func ParseTLV(data []byte) ([]TLV, error) {
	result := []TLV{}
	for len(data) > 0 {
		if len(data) < 4 {
			return nil, fmt.Errorf("truncated TLV header")
		}
		tag, n := binary.LittleEndian.Uint16(data), int(binary.LittleEndian.Uint16(data[2:]))
		data = data[4:]
		if len(data) < n {
			return nil, fmt.Errorf("truncated TLV tag %d", tag)
		}
		result = append(result, TLV{Tag: tag, Value: append([]byte{}, data[:n]...)})
		data = data[n:]
	}
	return result, nil
}

func decodeParams(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var object map[string]json.RawMessage
	if err := dec.Decode(&object); err != nil || object == nil {
		return nil, fmt.Errorf("params must be a JSON object")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("params must contain exactly one JSON object")
	}
	return object, nil
}

func encodeFields(fields []ParameterDefinition, layout string, raw json.RawMessage) ([]byte, error) {
	object, err := decodeParams(raw)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(fields))
	for _, f := range fields {
		known[f.Name] = true
	}
	for name := range object {
		if !known[name] {
			return nil, fmt.Errorf("unknown parameter %q", name)
		}
	}
	if layout == "" {
		layout = "positional"
	}
	if layout != "positional" && layout != "tlv" {
		return nil, fmt.Errorf("unknown layout %q", layout)
	}
	var payload []byte
	gap := false
	for _, field := range fields {
		value, present := object[field.Name]
		if !present {
			if field.Required {
				return nil, fmt.Errorf("parameter %s is required", field.Name)
			}
			if layout == "positional" {
				gap = true
			}
			continue
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("parameter %s cannot be null", field.Name)
		}
		if gap && layout == "positional" {
			return nil, fmt.Errorf("parameter %s follows an omitted positional field", field.Name)
		}
		values := []json.RawMessage{value}
		if field.Repeated {
			if err := json.Unmarshal(value, &values); err != nil || values == nil {
				return nil, fmt.Errorf("parameter %s must be an array", field.Name)
			}
			if len(values) < field.MinItems || (field.MaxItems > 0 && len(values) > field.MaxItems) {
				return nil, fmt.Errorf("parameter %s has an invalid item count", field.Name)
			}
		}
		for _, item := range values {
			encoded, err := encodeValue(field, item)
			if err != nil {
				return nil, fmt.Errorf("parameter %s: %w", field.Name, err)
			}
			if layout == "tlv" {
				if field.Tag == 0 {
					return nil, fmt.Errorf("parameter %s has no TLV tag", field.Name)
				}
				payload, err = AppendTLV(payload, field.Tag, encoded)
				if err != nil {
					return nil, err
				}
			} else {
				payload = append(payload, encoded...)
			}
		}
	}
	return payload, nil
}

func unsigned(raw json.RawMessage, allowString bool) (uint64, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return 0, err
	}
	var text string
	switch v := value.(type) {
	case json.Number:
		text = v.String()
	case string:
		if !allowString {
			return 0, fmt.Errorf("must be a JSON integer")
		}
		text = v
	default:
		return 0, fmt.Errorf("must be an unsigned integer")
	}
	if text == "" {
		return 0, fmt.Errorf("must be an unsigned integer")
	}
	for _, c := range text {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("must be an unsigned decimal integer")
		}
	}
	return strconv.ParseUint(text, 10, 64)
}

func numericWidth(kind string) int {
	switch kind {
	case "BYTE":
		return 1
	case "UINT16":
		return 2
	case "UINT32", "UNIXTIME", "IPV4":
		return 4
	case "UINT40":
		return 5
	}
	return 0
}

func encodeValue(f ParameterDefinition, raw json.RawMessage) ([]byte, error) {
	kind := f.WireType
	if width := numericWidth(kind); width > 0 || kind == "VLN" {
		n, err := unsigned(raw, kind == "VLN")
		if err != nil {
			return nil, err
		}
		if width > 0 && n > (uint64(1)<<(8*width))-1 {
			return nil, fmt.Errorf("value does not fit %s", kind)
		}
		if f.Min != nil && n < *f.Min || f.Max != nil && n > *f.Max {
			return nil, fmt.Errorf("value outside documented range")
		}
		if len(f.Enum) > 0 {
			if _, ok := f.Enum[strconv.FormatUint(n, 10)]; !ok {
				return nil, fmt.Errorf("value is not in the documented enumeration")
			}
		}
		if width == 0 {
			width = 1
			for temp := n; temp > 255; temp >>= 8 {
				width++
			}
		}
		data := make([]byte, width)
		for i := range data {
			data[i] = byte(n >> (8 * i))
		}
		return data, nil
	}
	switch kind {
	case "ASCII", "RAW", "HEX":
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, fmt.Errorf("must be a string")
		}
		var data []byte
		var err error
		if kind == "ASCII" {
			data, err = charmap.CodePage866.NewEncoder().Bytes([]byte(text))
		} else {
			data, err = hex.DecodeString(text)
		}
		if err != nil {
			return nil, fmt.Errorf("cannot encode %s: %w", kind, err)
		}
		if len(data) < f.MinLength || f.MaxLength > 0 && len(data) > f.MaxLength {
			return nil, fmt.Errorf("encoded byte length outside documented bounds")
		}
		if f.FixedLength > 0 {
			if f.Padding == "space" && len(data) <= f.FixedLength {
				data = append(data, bytes.Repeat([]byte{' '}, f.FixedLength-len(data))...)
			}
			if len(data) != f.FixedLength {
				return nil, fmt.Errorf("must encode to exactly %d bytes", f.FixedLength)
			}
		}
		return data, nil
	case "DATETIME3", "DATETIME5":
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, fmt.Errorf("must be a device-local calendar string")
		}
		format := "2006-01-02"
		if kind == "DATETIME5" {
			format = "2006-01-02T15:04"
		}
		date, err := time.Parse(format, text)
		if err != nil || date.Year() < 2000 || date.Year() > 2255 {
			return nil, fmt.Errorf("must match %s with year 2000..2255 and no timezone", format)
		}
		data := []byte{byte(date.Year() - 2000), byte(date.Month()), byte(date.Day())}
		if kind == "DATETIME5" {
			data = append(data, byte(date.Hour()), byte(date.Minute()))
		}
		return data, nil
	case "FVLN":
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, fmt.Errorf("decimal values must be strings to preserve precision")
		}
		parts := strings.Split(text, ".")
		if len(parts) > 2 || parts[0] == "" || len(parts) == 2 && parts[1] == "" {
			return nil, fmt.Errorf("invalid unsigned decimal string")
		}
		scale := 0
		if len(parts) == 2 {
			scale = len(parts[1])
		}
		if scale > 255 {
			return nil, fmt.Errorf("decimal scale exceeds 255")
		}
		digits := strings.Join(parts, "")
		for _, c := range digits {
			if c < '0' || c > '9' {
				return nil, fmt.Errorf("invalid unsigned decimal string")
			}
		}
		digits = strings.TrimLeft(digits, "0")
		if digits == "" {
			digits = "0"
		}
		n, err := strconv.ParseUint(digits, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("decimal mantissa exceeds 64 bits")
		}
		data := []byte{byte(scale), byte(n)}
		for n >>= 8; n > 0; n >>= 8 {
			data = append(data, byte(n))
		}
		return data, nil
	case "STLV", "STRUCT":
		layout := "tlv"
		if kind == "STRUCT" {
			layout = "positional"
		}
		return encodeFields(f.Children, layout, raw)
	default:
		return nil, fmt.Errorf("unsupported wire type %s", kind)
	}
}

func fixedWidth(f ParameterDefinition) int {
	if n := numericWidth(f.WireType); n > 0 {
		return n
	}
	switch f.WireType {
	case "DATETIME3":
		return 3
	case "DATETIME5":
		return 5
	}
	return f.FixedLength
}

func decodeFields(fields []ParameterDefinition, layout string, data []byte) (map[string]any, error) {
	if layout == "tlv" {
		return decodeTLVFields(fields, data)
	}
	result := map[string]any{}
	for i, f := range fields {
		n := fixedWidth(f)
		if len(data) == 0 && !f.Required {
			continue
		}
		if n == 0 {
			if i != len(fields)-1 {
				return nil, fmt.Errorf("variable-width field %s must be last", f.Name)
			}
			n = len(data)
		}
		if len(data) < n {
			return nil, fmt.Errorf("truncated field %s", f.Name)
		}
		value, err := decodeValue(f, data[:n])
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", f.Name, err)
		}
		result[f.Name] = value
		data = data[n:]
	}
	if len(data) != 0 {
		return nil, fmt.Errorf("unexpected %d trailing response bytes", len(data))
	}
	return result, nil
}

func decodeTLVFields(fields []ParameterDefinition, data []byte) (map[string]any, error) {
	items, err := ParseTLV(data)
	if err != nil {
		return nil, err
	}
	byTag := make(map[uint16]ParameterDefinition, len(fields))
	for _, f := range fields {
		byTag[f.Tag] = f
	}
	result := map[string]any{}
	unknown := []map[string]any{}
	for _, item := range items {
		f, known := byTag[item.Tag]
		if !known {
			unknown = append(unknown, map[string]any{"tag": item.Tag, "valueHex": hex.EncodeToString(item.Value)})
			continue
		}
		if _, exists := result[f.Name]; exists && !f.Repeated {
			return nil, fmt.Errorf("duplicate response tag %d", item.Tag)
		}
		value, err := decodeValue(f, item.Value)
		if err != nil {
			return nil, fmt.Errorf("tag %d: %w", item.Tag, err)
		}
		if f.Repeated {
			values, _ := result[f.Name].([]any)
			result[f.Name] = append(values, value)
		} else {
			result[f.Name] = value
		}
	}
	for _, f := range fields {
		if f.Required {
			if _, ok := result[f.Name]; !ok {
				return nil, fmt.Errorf("required response tag %d missing", f.Tag)
			}
		}
	}
	if len(unknown) > 0 {
		result["unknownTags"] = unknown
	}
	return result, nil
}

func decodeValue(f ParameterDefinition, data []byte) (any, error) {
	if width := numericWidth(f.WireType); width > 0 {
		if len(data) != width {
			return nil, fmt.Errorf("expected %d bytes", width)
		}
		var n uint64
		for i, b := range data {
			n |= uint64(b) << (8 * i)
		}
		if f.WireType == "IPV4" {
			return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}).String(), nil
		}
		return n, nil
	}
	switch f.WireType {
	case "ASCII":
		if f.FixedLength > 0 && len(data) != f.FixedLength {
			return nil, fmt.Errorf("expected %d bytes", f.FixedLength)
		}
		if len(data) < f.MinLength || f.MaxLength > 0 && len(data) > f.MaxLength {
			return nil, fmt.Errorf("invalid encoded string length")
		}
		decoded, err := charmap.CodePage866.NewDecoder().Bytes(data)
		if err != nil {
			return nil, err
		}
		text := string(decoded)
		if f.Padding == "space" {
			text = strings.TrimRight(text, " ")
		}
		return text, nil
	case "RAW", "HEX":
		if f.FixedLength > 0 && len(data) != f.FixedLength {
			return nil, fmt.Errorf("expected %d bytes", f.FixedLength)
		}
		return hex.EncodeToString(data), nil
	case "VLN":
		if len(data) < 1 || len(data) > 8 {
			return nil, fmt.Errorf("VLN must contain 1..8 bytes")
		}
		var n uint64
		for i, b := range data {
			n |= uint64(b) << (8 * i)
		}
		return n, nil
	case "FVLN":
		if len(data) < 2 || len(data) > 9 {
			return nil, fmt.Errorf("FVLN must contain a scale and 1..8 mantissa bytes")
		}
		var n uint64
		for i, b := range data[1:] {
			n |= uint64(b) << (8 * i)
		}
		text := strconv.FormatUint(n, 10)
		scale := int(data[0])
		if scale == 0 {
			return text, nil
		}
		if len(text) <= scale {
			text = strings.Repeat("0", scale-len(text)+1) + text
		}
		return text[:len(text)-scale] + "." + text[len(text)-scale:], nil
	case "DATETIME3", "DATETIME5":
		n := 3
		format := "2006-01-02"
		if f.WireType == "DATETIME5" {
			n = 5
			format = "2006-01-02T15:04"
		}
		if len(data) != n {
			return nil, fmt.Errorf("expected %d date bytes", n)
		}
		if bytes.Equal(data, make([]byte, n)) && f.AllowZeroDate {
			return nil, nil
		}
		text := fmt.Sprintf("%04d-%02d-%02d", 2000+int(data[0]), data[1], data[2])
		if n == 5 {
			text += fmt.Sprintf("T%02d:%02d", data[3], data[4])
		}
		if _, err := time.Parse(format, text); err != nil {
			return nil, fmt.Errorf("invalid device calendar date")
		}
		return text, nil
	case "STLV":
		return decodeTLVFields(f.Children, data)
	case "STRUCT":
		return decodeFields(f.Children, "positional", data)
	default:
		return nil, fmt.Errorf("unsupported wire type %s", f.WireType)
	}
}

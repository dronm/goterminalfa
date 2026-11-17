package service

import (
	"fmt"
	"net/mail"
	"strings"

	"github.com/dronm/goterminalfa/internal/protocol"
)

// validateRussianINN checks the decimal form and both control digits for a
// twelve-digit INN, or the single control digit for a ten-digit INN. It does not
// query issuance or maintain region-prefix restrictions.
func validateRussianINN(value string, lengths ...int) error {
	validLength := false
	for _, length := range lengths {
		validLength = validLength || len(value) == length
	}
	if !validLength {
		return fmt.Errorf("must contain %v decimal digits", lengths)
	}
	if !registrationDecimal(value) {
		return fmt.Errorf("must contain ASCII decimal digits only")
	}
	if strings.Trim(value, "0") == "" {
		return fmt.Errorf("all-zero INN is invalid")
	}
	check := func(weights []int, position int) bool {
		sum := 0
		for index, weight := range weights {
			sum += int(value[index]-'0') * weight
		}
		return byte(sum%11%10) == value[position]-'0'
	}
	valid := false
	switch len(value) {
	case 10:
		valid = check([]int{2, 4, 10, 3, 5, 9, 4, 6, 8}, 9)
	case 12:
		valid = check([]int{7, 2, 4, 10, 3, 5, 9, 4, 6, 8}, 10) && check([]int{3, 7, 2, 4, 10, 3, 5, 9, 4, 6, 8}, 11)
	}
	if !valid {
		return fmt.Errorf("INN checksum is invalid")
	}
	return nil
}

func registrationDecimal(value string) bool {
	if value == "" {
		return false
	}
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

// validateRegistrationNumber validates the public sixteen-digit RN KKT against
// the actual factory serial read from command 02, never a caller-supplied FN
// serial. CRC input uses zero padding, whereas E3 fields use space padding.
func validateRegistrationNumber(registrationNumber, userInn, serial string) error {
	if len(registrationNumber) != 16 || !registrationDecimal(registrationNumber) {
		return fmt.Errorf("registrationNumber must contain exactly sixteen ASCII decimal digits")
	}
	if err := validateRussianINN(userInn, 10, 12); err != nil {
		return fmt.Errorf("userInn: %w", err)
	}
	if len(serial) > 20 || !registrationDecimal(serial) {
		return fmt.Errorf("KKT factory serial must contain one to twenty ASCII decimal digits to validate registrationNumber")
	}
	input := registrationNumber[:10] + strings.Repeat("0", 12-len(userInn)) + userInn + strings.Repeat("0", 20-len(serial)) + serial
	expected := fmt.Sprintf("%06d", protocol.CRC16([]byte(input)))
	if registrationNumber[10:] != expected {
		return fmt.Errorf("registrationNumber checksum does not match userInn and the actual KKT factory serial")
	}
	return nil
}

func validateRegistrationEmail(value string) error {
	if value == "" || value != strings.TrimSpace(value) || strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("must be one bare email address without surrounding whitespace or line breaks")
	}
	// The parser accepts display-name/angle-address syntax and comments. Reject
	// those outside a quoted local part while preserving valid quoted mailbox
	// syntax and the caller's exact local-part case on the wire.
	quoted, escaped := false, false
	for _, character := range value {
		if escaped {
			escaped = false
			continue
		}
		if quoted && character == '\\' {
			escaped = true
			continue
		}
		if character == '"' {
			quoted = !quoted
			continue
		}
		if !quoted && strings.ContainsRune("<>(),;", character) {
			return fmt.Errorf("must be one bare email address without display names, comments or lists")
		}
	}
	address, err := mail.ParseAddress(value)
	if err != nil || address.Name != "" {
		return fmt.Errorf("must be one valid bare email address")
	}
	return nil
}

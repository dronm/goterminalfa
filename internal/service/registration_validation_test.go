package service

import "testing"

func TestRussianINNChecksumsAndDecimalForm(t *testing.T) {
	for _, value := range []string{"7707083893", "500100732259", "123456789047", "0012345673"} {
		if err := validateRussianINN(value, 10, 12); err != nil {
			t.Errorf("valid INN %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{
		"", "770708389", "77070838930", "7707083894", "500100732249", "500100732258",
		"0000000000", "000000000000", "770708389X", " 7707083893", "７７０７０８３８９３", "7707083893 ",
	} {
		if err := validateRussianINN(value, 10, 12); err == nil {
			t.Errorf("invalid INN %q accepted", value)
		}
	}
	if err := validateRussianINN("7707083893", 12); err == nil {
		t.Fatal("representative's twelve-digit INN restriction was ignored")
	}
}

func TestRegistrationNumberIndependentCRCFixtures(t *testing.T) {
	// Fixed vectors cross-checked independently with Python binascii.crc_hqx
	// (initial 0xFFFF), rather than constructed using this implementation.
	for _, tc := range []struct{ rn, inn, serial string }{
		{"0000000001007378", "7707083893", "123456789012"},
		{"1234567890049807", "500100732259", "000000000001"},
		{"0000000001003315", "7707083893", "123456789013"},
		{"0000000001046446", "7707083893", "550101046182"},
	} {
		if err := validateRegistrationNumber(tc.rn, tc.inn, tc.serial); err != nil {
			t.Fatalf("RN %q rejected: %v", tc.rn, err)
		}
	}
	for _, tc := range []struct{ rn, inn, serial string }{
		{"0000000001007379", "7707083893", "123456789012"},
		{"0000000001007378", "7707083893", "123456789013"},
		{"0000000001007378", "500100732259", "123456789012"},
		{"0000000001007378", "7707083894", "123456789012"},
		{"0000000001007378", "7707083893", "12345678901X"},
		{"0000000001007378", "7707083893", "123456789012345678901"},
		{"0000000001007378", "7707083893", ""},
		{"000000000100737", "7707083893", "123456789012"},
		{"0000000001007378    ", "7707083893", "123456789012"},
		{"000000000100737X", "7707083893", "123456789012"},
		{"0000000000000000", "7707083893", "123456789012"},
	} {
		if err := validateRegistrationNumber(tc.rn, tc.inn, tc.serial); err == nil {
			t.Errorf("invalid RN tuple accepted: %+v", tc)
		}
	}
}

func TestRegistrationEmailAcceptsOnlySingleBareMailbox(t *testing.T) {
	for _, value := range []string{"Sales@example.com", "sales+receipts@example.test", `"first last"@example.com`, `"a,b"@example.com`, "sales@[127.0.0.1]"} {
		if err := validateRegistrationEmail(value); err != nil {
			t.Errorf("valid bare email %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{
		"", "email", "a@@example.com", "a@", "@example.com", "a b@example.com", "a@example..com",
		"Name <a@example.com>", "<a@example.com>", "a@example.com,b@example.com", "a@example.com; b@example.com",
		"a@example.com (comment)", " a@example.com", "a@example.com ", "a@example.com\r\nBcc: b@example.com",
	} {
		if err := validateRegistrationEmail(value); err == nil {
			t.Errorf("invalid bare email %q accepted", value)
		}
	}
}

package service

import (
	"encoding/json"
	"fmt"
)

const ffd12RegistrationReasonMask uint64 = 0x803E7FFF

// ffd12RegistrationCommands defines the supplement's registration report. The
// application command validates and runs the complete E1/E2/E3 procedure.
func ffd12RegistrationCommands() []Command {
	dataFields := []ParameterDefinition{
		{Name: "userName", TitleRU: "Наименование пользователя", Description: "Registered user's name, at most 128 CP866 bytes.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Required: true, Tag: 1048, MinLength: 1, MaxLength: 128},
		{Name: "settlementAddress", TitleRU: "Адрес расчетов", Description: "Settlement address, at most 256 CP866 bytes.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Required: true, Tag: 1009, MinLength: 1, MaxLength: 256},
		{Name: "settlementPlace", TitleRU: "Место расчетов", Description: "Settlement place, at most 256 CP866 bytes.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Required: true, Tag: 1187, MinLength: 1, MaxLength: 256},
		{Name: "representativeName", TitleRU: "ФИО уполномоченного лица", Description: "Authorized representative's full name, at most 64 CP866 bytes.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Required: true, Tag: 1021, MinLength: 1, MaxLength: 64},
		{Name: "representativeInn", TitleRU: "ИНН уполномоченного лица", Description: "Optional representative INN: twelve decimal digits with valid checksums.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 1203, FixedLength: 12},
		{Name: "ofdInn", TitleRU: "ИНН ОФД", Description: "OFD INN: ten or twelve decimal digits with valid checksums, right-padded with spaces to twelve wire bytes. Omitted from autonomous-mode requests.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 1017, FixedLength: 12, Padding: "space", RequiredIf: "Required unless operatingMode explicitly selects autonomous mode (bit 1)."},
		{Name: "ofdName", TitleRU: "Наименование ОФД", Description: "OFD name, at most 64 CP866 bytes. Omitted from autonomous-mode requests.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 1046, MinLength: 1, MaxLength: 64, RequiredIf: "Required unless operatingMode explicitly selects autonomous mode (bit 1)."},
		{Name: "senderEmail", TitleRU: "Электронный адрес отправителя чеков", Description: "One bare sender email address, at most 64 CP866 bytes; display names and address lists are rejected. Omitted from autonomous-mode requests.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 1117, MinLength: 1, MaxLength: 64, RequiredIf: "Required unless operatingMode explicitly selects autonomous mode (bit 1)."},
		{Name: "machineNumber", TitleRU: "Номер автоматического устройства расчетов", Description: "Optional automatic settlement device number, at most 21 CP866 bytes.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 1036, MinLength: 1, MaxLength: 21},
		{Name: "reportAttribute", TitleRU: "Дополнительный реквизит отчета о регистрации", Description: "Optional additional report attribute, at most 32 CP866 bytes; include reportDataHex together with this field.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 1274, MaxLength: 32, RequiredIf: "Required when reportDataHex is present."},
		{Name: "reportDataHex", TitleRU: "Дополнительные данные отчета о регистрации", Description: "Optional report data as hexadecimal, at most 32 decoded bytes; include reportAttribute together with this field.", JSONType: "string", WireType: "HEX", Tag: 1275, MaxLength: 32, RequiredIf: "Required when reportAttribute is present."},
		{Name: "applicationFlags", TitleRU: "Признаки условий применения ККТ", Description: "Optional application-condition bitmask, bits 0 through 6; omission is preserved and is distinct from explicit zero.", JSONType: "integer", WireType: "BYTE", Tag: 9998, Bits: map[string]string{"0": "Excise goods", "1": "Gambling", "2": "Lottery", "3": "Printer installed in vending device", "4": "Marked goods", "5": "Pawnshop activity", "6": "Insurance activity"}},
		{Name: "operatingMode", TitleRU: "Режим работы", Description: "Optional operating-mode bitmask. Autonomous mode excludes encryption and internet use; BSO mode requires services. No missing mode is inferred.", JSONType: "integer", WireType: "BYTE", Tag: 9999, Bits: map[string]string{"0": "Encryption", "1": "Autonomous mode", "3": "Services", "4": "Strict-reporting forms (BSO)", "5": "Internet"}},
	}
	data := makeCommand(commandSpec{
		Code: 0xE2, Name: "sendRegistrationData", TitleRU: "Передать данные отчета о регистрации", Category: "registration", Page: 7, RequestLayout: "tlv",
		Description:   "Supply FFD 1.2 registration report TLVs after E1. Tags 1203, 1036, 1274, 1275, 9998 and 9999 are optional. OFD fields are required unless autonomous mode is explicitly selected; any supplied OFD values are validated before autonomous mode omits their tags.",
		DescriptionRU: "Передать данные отчета о регистрации (0xE2). Теги 1203, 1036, 1274, 1275, 9998 и 9999 необязательны. Теги 1274 и 1275 передаются совместно. ИНН проверяются по контрольным разрядам. В автономном режиме теги 1017, 1046 и 1117 не передаются; вне автономного режима они обязательны. Признак БСО требует признака услуг; автономный режим несовместим с шифрованием и применением в интернет.",
		Input:         dataFields,
		Validate:      validateFFD12RegistrationData,
		Encode: func(raw json.RawMessage) ([]byte, error) {
			params, err := decodeParams(raw)
			if err != nil {
				return nil, err
			}
			if mode, present := params["operatingMode"]; present {
				value, err := unsigned(mode, false)
				if err != nil {
					return nil, err
				}
				if value&2 != 0 {
					delete(params, "ofdInn")
					delete(params, "ofdName")
					delete(params, "senderEmail")
				}
			}
			encoded, err := json.Marshal(params)
			if err != nil {
				return nil, err
			}
			return encodeFields(dataFields, "tlv", encoded)
		},
		Notes:         []string{"Optional fields are never replaced by defaults; explicitly supplied zero flags are transmitted.", "INN validation checks decimal shape and control digits, not registry issuance. Email validation checks syntax, not mailbox deliverability."},
		Preconditions: []string{"E1 must have succeeded in the same complete registration procedure.", "Complete the procedure with E3 under the same device lock."},
	})
	commands := []Command{
		makeCommand(commandSpec{
			Code: 0xE1, Name: "startRegistration", TitleRU: "Начать отчет о регистрации/перерегистрации ККТ", Category: "registration", Page: 7,
			Description:   "Begin the FFD 1.2 registration procedure: 0 initial registration, 1 re-registration replacing the FN, 2 re-registration without FN replacement.",
			DescriptionRU: "Начать отчет о регистрации/перерегистрации ККТ (0xE1). Тип отчета: 0 — первоначальная регистрация ККТ; 1 — перерегистрация в связи с заменой ФН; 2 — перерегистрация без замены ФН. После успешного выполнения передаются E2 и E3 в одной операции.",
			Input:         []ParameterDefinition{{Name: "reportType", TitleRU: "Тип отчета", Description: "0 initial registration; 1 replacing the FN; 2 without FN replacement.", JSONType: "integer", WireType: "BYTE", Required: true, Enum: map[string]string{"0": "Initial registration", "1": "Re-registration replacing the FN", "2": "Re-registration without FN replacement"}}},
			Preconditions: []string{"Validate all registration parameters, including the RN KKT checksum against the actual KKT factory serial, before starting this procedure."},
		}),
		data,
		makeCommand(commandSpec{
			Code: 0xE3, Name: "completeRegistration", TitleRU: "Сформировать отчет о регистрации/перерегистрации", Category: "registration", Page: 8,
			Description:   "Finalize FFD 1.2 registration after E1/E2. User INN is right-padded to twelve bytes; the sixteen-digit RN KKT is right-padded to twenty bytes. The optional reason is a four-byte little-endian UINT32 bitmask, omitted for initial registration.",
			DescriptionRU: "Сформировать отчет о регистрации/перерегистрации (0xE3). ИНН пользователя дополняется пробелами справа до 12 байт. РН ККТ содержит 16 цифр и дополняется пробелами справа до 20 байт. Причина изменения сведений о ККТ — UINT32 LE, битовая маска тега 1205, четыре байта; при первоначальной регистрации отсутствует. Выходные данные: номер ФД UINT32 LE и фискальный признак UINT32 LE.",
			Input: []ParameterDefinition{
				{Name: "userInn", TitleRU: "ИНН пользователя", Description: "User INN: ten or twelve decimal digits with valid checksums, right-padded with spaces to twelve wire bytes.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Required: true, FixedLength: 12, Padding: "space"},
				{Name: "registrationNumber", TitleRU: "Регистрационный номер ККТ", Description: "Sixteen decimal digits: ten sequence digits followed by the six-digit decimal CRC16. Validated against userInn and actual KKT factory serial before E1; right-padded with four spaces on the wire.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Required: true, FixedLength: 20, Padding: "space"},
				{Name: "taxationSystems", TitleRU: "Режим налогообложения", Description: "Nonzero taxation-system bitmask; only bits 0, 1, 2, 4 and 5 are defined.", JSONType: "integer", WireType: "BYTE", Required: true, Bits: map[string]string{"0": "General taxation", "1": "Simplified income", "2": "Simplified income minus expenses", "4": "Unified agricultural tax", "5": "Patent taxation"}},
				{Name: "reregistrationReason", TitleRU: "Причина изменения сведений о ККТ", Description: "Four-byte UINT32 LE bitmask of tag 1205: bits 0–14, 17–21 and 31. Other reasons (bit 31) is decimal 2147483648. Omit for reportType 0; required for reportType 1 or 2.", JSONType: "integer", WireType: "UINT32", Min: limit(1), Bits: ffd12RegistrationReasonBits(), RequiredIf: "Required for reportType 1 or 2; must be omitted for reportType 0."},
			},
			Output:        registrationFiscalResult(),
			Validate:      validateFFD12RegistrationCompletion,
			Notes:         []string{"The supplement labels the reason field BYTE but defines bit positions through 31. The corrected wire representation is UINT32 LE (four bytes), matching tag 1205 and the FN FFD 1.2 protocol.", "RN KKT CRC16 uses polynomial 0x1021, initial 0xFFFF, no reflection or final XOR, over ten sequence digits + twelve-digit left-zero-padded user INN + twenty-digit left-zero-padded KKT factory serial. This checksum padding differs from command-field space padding."},
			Preconditions: []string{"E1 and E2 must have completed successfully in the same registration procedure.", "The reason presence must match the E1 report type."},
		}),
	}
	for i := range commands {
		commands[i].Definition.Source = ffd12ExtraSource(commands[i].Definition.Source.Page)
		commands[i].Definition.OperationRequired = true
	}
	return commands
}

func validateFFD12RegistrationData(params map[string]json.RawMessage) error {
	for _, field := range []struct {
		name    string
		lengths []int
	}{{"representativeInn", []int{12}}, {"ofdInn", []int{10, 12}}} {
		if value, present := params[field.name]; present {
			var text string
			if err := json.Unmarshal(value, &text); err != nil {
				return fmt.Errorf("%s must be a string", field.name)
			}
			if err := validateRussianINN(text, field.lengths...); err != nil {
				return fmt.Errorf("%s: %w", field.name, err)
			}
		}
	}
	if email, present := params["senderEmail"]; present {
		var text string
		if err := json.Unmarshal(email, &text); err != nil {
			return fmt.Errorf("senderEmail must be a string")
		}
		if err := validateRegistrationEmail(text); err != nil {
			return fmt.Errorf("senderEmail: %w", err)
		}
	}
	if err := registrationMask(params, "applicationFlags", 0x7F, false); err != nil {
		return err
	}
	if err := registrationMask(params, "operatingMode", 0x3B, false); err != nil {
		return err
	}
	var mode uint64
	if value, present := params["operatingMode"]; present {
		var err error
		mode, err = unsigned(value, false)
		if err != nil {
			return fmt.Errorf("operatingMode: %w", err)
		}
	}
	if mode&2 != 0 && mode&0x21 != 0 {
		return fmt.Errorf("operatingMode: autonomous mode cannot be combined with encryption or internet use")
	}
	if mode&0x10 != 0 && mode&0x08 == 0 {
		return fmt.Errorf("operatingMode: BSO mode requires services mode")
	}
	if mode&2 == 0 {
		for _, name := range []string{"ofdInn", "ofdName", "senderEmail"} {
			if _, present := params[name]; !present {
				return fmt.Errorf("%s is required unless operatingMode explicitly selects autonomous mode", name)
			}
		}
	}
	_, attribute := params["reportAttribute"]
	_, reportData := params["reportDataHex"]
	if attribute != reportData {
		return fmt.Errorf("reportAttribute and reportDataHex must be supplied together (tags 1274 and 1275)")
	}
	return nil
}

func validateFFD12RegistrationCompletion(params map[string]json.RawMessage) error {
	var inn string
	if err := json.Unmarshal(params["userInn"], &inn); err != nil {
		return fmt.Errorf("userInn must be a string")
	}
	if err := validateRussianINN(inn, 10, 12); err != nil {
		return fmt.Errorf("userInn: %w", err)
	}
	if err := registrationDigits(params, "registrationNumber", 16); err != nil {
		return err
	}
	if err := registrationMask(params, "taxationSystems", 0x37, true); err != nil {
		return err
	}
	return registrationMask(params, "reregistrationReason", ffd12RegistrationReasonMask, true)
}

func registrationMask(params map[string]json.RawMessage, name string, allowed uint64, nonzero bool) error {
	value, present := params[name]
	if !present {
		return nil
	}
	number, err := unsigned(value, false)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if number&^allowed != 0 {
		return fmt.Errorf("%s contains undefined bits (allowed mask 0x%X)", name, allowed)
	}
	if nonzero && number == 0 {
		return fmt.Errorf("%s must contain at least one defined bit", name)
	}
	return nil
}

func ffd12RegistrationReasonBits() map[string]string {
	return map[string]string{
		"0": "FN replacement", "1": "OFD replacement", "2": "User name changed", "3": "Installation address or place changed",
		"4": "Autonomous to data-transfer mode", "5": "Data-transfer to autonomous mode", "6": "KKT model version changed", "7": "Taxation systems changed",
		"8": "Automatic settlement device number changed", "9": "Automatic to cashier operation", "10": "Cashier to automatic operation", "11": "Enable BSO",
		"12": "Disable BSO", "13": "Internet-only to printing mode", "14": "Printing to internet-only mode", "17": "Disable gambling", "18": "Enable gambling",
		"19": "Disable lottery", "20": "Enable lottery", "21": "FFD version changed", "31": "Other reasons",
	}
}

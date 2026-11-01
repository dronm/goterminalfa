package service

import (
	"encoding/json"
	"fmt"
)

// registrationCommands describes the registration and shift commands whose
// layouts are specified in protocol 01.05. Document-opening and completion
// commands are deliberately separate; the service owns their operation context.
func registrationCommands() []Command {
	registrationInput := []ParameterDefinition{
		{Name: "userName", TitleRU: "Наименование пользователя", Description: "Registered user's name, at most 128 CP866 bytes.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Required: true, Tag: 1048, MinLength: 1, MaxLength: 128},
		{Name: "settlementAddress", TitleRU: "Адрес расчетов", Description: "Settlement address; omitted registration values must be supplied per receipt using 0x1F.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 1009, MinLength: 1, MaxLength: 164, RequiredIf: "Required unless the settlement address is intentionally assigned per receipt under the multiple-vending-device mode described on page 19."},
		{Name: "settlementPlace", TitleRU: "Место расчетов", Description: "Settlement location; omitted registration values must be supplied per receipt.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 1187, MinLength: 1, MaxLength: 64, RequiredIf: "Required unless the settlement place is intentionally assigned per receipt as described on page 19."},
		{Name: "representativeName", TitleRU: "ФИО уполномоченного лица", Description: "Authorized representative's full name.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Required: true, Tag: 1021, MinLength: 1, MaxLength: 64},
		{Name: "representativeInn", TitleRU: "ИНН уполномоченного лица", Description: "Authorized representative's INN, exactly 12 decimal digits.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Required: true, Tag: 1203, FixedLength: 12},
		{Name: "ofdInn", TitleRU: "ИНН ОФД", Description: "OFD INN, 10 or 12 decimal digits, right-padded with spaces to twelve bytes.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 1017, FixedLength: 12, Padding: "space", RequiredIf: "Required unless operatingMode has autonomous-mode bit 1 set."},
		{Name: "ofdName", TitleRU: "Наименование ОФД", Description: "OFD name; omitted or ignored in autonomous mode.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 1046, MinLength: 1, MaxLength: 64, RequiredIf: "Required unless operatingMode has autonomous-mode bit 1 set."},
		{Name: "senderEmail", TitleRU: "Электронный адрес отправителя чеков", Description: "Receipt sender's email; omitted or ignored in autonomous mode.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 1117, MinLength: 1, MaxLength: 64, RequiredIf: "Required unless operatingMode has autonomous-mode bit 1 set."},
		{Name: "machineNumber", TitleRU: "Номер автоматического устройства расчетов", Description: "Vending device number, at most 21 encoded bytes; see the page-13/page-19 registration-presence notes.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 1036, MinLength: 1, MaxLength: 21, RequiredIf: "Required for ordinary Terminal FA registration (page 13); may be omitted when assigned per receipt in multiple-device operation (page 19)."},
		{Name: "agentFlags", TitleRU: "Признак платежного агента", Description: "Bitmask of possible agent roles; see Appendix 7.", JSONType: "integer", WireType: "BYTE", Required: true, Tag: 1057, Bits: registrationAgentBits()},
		{Name: "operatingMode", TitleRU: "Режим работы", Description: "Service tag 9999 containing operating-mode flags. Terminal FA is registered in automatic mode regardless of this byte.", JSONType: "integer", WireType: "BYTE", Required: true, Tag: 9999, Bits: registrationModeBits()},
	}
	registrationFinishInput := []ParameterDefinition{
		{Name: "userInn", TitleRU: "ИНН пользователя", Description: "Registered user's INN, 10 or 12 decimal digits, right-padded with spaces to twelve bytes.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Required: true, FixedLength: 12, Padding: "space"},
		{Name: "registrationNumber", TitleRU: "Регистрационный номер ККТ (РН ККТ)", Description: "KKT registration number: exactly twenty encoded bytes, as specified by this protocol revision.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Required: true, FixedLength: 20},
		{Name: "taxationSystems", TitleRU: "Режим налогообложения", Description: "Bitmask of permitted taxation systems, Appendix 7.", JSONType: "integer", WireType: "BYTE", Required: true, Bits: registrationTaxationBits()},
		{Name: "reregistrationReason", TitleRU: "Код причины перерегистрации", Description: "Omit for initial registration. Include for re-registration: 1 FN replacement, 2 OFD replacement, 3 changed details, 4 changed details and settings.", JSONType: "integer", WireType: "BYTE", RequiredIf: "Required when finalizing a re-registration started with reportType 1 or 2; omitted for reportType 0.", Enum: map[string]string{"1": "FN replacement", "2": "OFD replacement", "3": "Changed registration details", "4": "Changed details and settings"}},
	}

	return []Command{
		makeCommand(commandSpec{
			Code: 0x10, Name: "cancelDocument", TitleRU: "Отменить текущий документ", Category: "documents", Page: 15,
			Description:   "Cancel any previously started fiscal document and discard all document data already supplied.",
			DescriptionRU: "Команда отменяет любой ранее начатый фискальный документ. Все данные, введенные с помощью команды «Передать данные документа», удаляются. Входные параметры: нет. Выходные данные: нет.",
			Input:         []ParameterDefinition{}, Output: []ParameterDefinition{},
		}),
		makeCommand(commandSpec{
			Code: 0x12, Name: "startRegistration", TitleRU: "Начать отчет о регистрации/перерегистрации ККТ", Category: "registration", Page: 13,
			Description:   "Start a registration or re-registration report. Further registration data must be supplied before finalizing with 0x13.",
			DescriptionRU: "Команда начинает процедуру регистрации/перерегистрации ККТ. Тип отчета: 0 — первоначальная регистрация ККТ; 1 — перерегистрация в связи с заменой ФН; 2 — перерегистрация без замены ФН. Выходные данные: нет.",
			Input:         []ParameterDefinition{{Name: "reportType", TitleRU: "Тип отчета", Description: "0 initial registration; 1 re-registration replacing the FN; 2 re-registration without FN replacement.", JSONType: "integer", WireType: "BYTE", Required: true, Enum: map[string]string{"0": "Initial registration", "1": "Re-registration replacing the FN", "2": "Re-registration without FN replacement"}}},
			Output:        []ParameterDefinition{},
		}),
		makeCommand(commandSpec{
			Code: 0x13, Name: "completeRegistration", TitleRU: "Сформировать отчет о регистрации/перерегистрации", Category: "registration", Page: 13,
			Description:   "Finalize the previously started registration or re-registration report. The optional trailing reason byte must be omitted for initial registration.",
			DescriptionRU: "Команда завершает процесс регистрации (перерегистрации) ККТ. ИНН пользователя дополняется пробелами до длины 12 символов. РН ККТ — 20 символов. Режим налогообложения — битовая маска. В случае первоначальной регистрации ККТ параметр «Код причины перерегистрации» в команду не включается. Выходные данные: номер ФД UINT32 LE, фискальный признак UINT32 LE.",
			Input:         registrationFinishInput, Output: registrationFiscalResult(),
			Preconditions: []string{"A registration/re-registration report must have been started with 0x12 and all necessary report data supplied with 0x16."},
			Notes:         []string{"The current request alone does not identify whether the previous 0x12 used reportType 0, 1 or 2. The caller must omit or include reregistrationReason consistently with that report.", "Registration-number width follows protocol 01.05; do not silently pad or truncate a shorter value."},
			Validate:      func(params map[string]json.RawMessage) error { return registrationDigits(params, "userInn", 10, 12) },
		}),
		makeCommand(commandSpec{
			Code: 0x16, Name: "sendRegistrationData", TitleRU: "Передать данные отчета о регистрации", Category: "registration", Page: 13, RequestLayout: "tlv",
			Description:   "Supply the TLV data for a registration/re-registration report. OFD fields are required outside autonomous mode; omitted vending-specific registration fields must be provided per receipt where the documented multi-device mode permits this.",
			DescriptionRU: "Для выполнения данной команды необходимо предварительное выполнение команды «Начать отчет о регистрации/перерегистрации ККТ» (0x12). Тэги 1017, 1046, 1117 не указываются (игнорируются ККТ) в случае регистрации в автономном режиме. Тэг 1036 указывается в случае регистрации ККТ в составе автоматического устройства расчетов (на странице 13 для Терминал-ФА указан как обязательный). На странице 19 допускается регистрация без адреса расчетов, места расчета и номера автомата с передачей отсутствующих тэгов в чеке командой 0x1F. Тэг 1057: биты выставляются в соответствии с возможными типами агента. Тэг 9999: режим работы, служебный тэг; значения битов приведены в приложении 7. Независимо от значения в тэге 9999 ККТ «Терминал-ФА» будет зарегистрирована в автоматическом режиме. Выходные данные: нет.",
			Input:         registrationInput, Output: []ParameterDefinition{},
			Preconditions: []string{"A registration/re-registration report must have been started successfully with 0x12."},
			Notes:         []string{"The manual's page 13 and page 19 describe different presence requirements for vending-specific registration fields. Their omission is supported for the per-receipt assignment mode; use 0x1F as required by the device's registration.", "The service does not infer the applicable registration mode from a missing field; supply the fields required by the intended device configuration."},
			Validate:      registrationDataValidate,
		}),
		makeCommand(commandSpec{
			Code: 0x20, Name: "getShiftParameters", TitleRU: "Запрос параметров текущей смены", Category: "shift", Page: 14, ReadOnly: true,
			Description:   "Read whether the shift is open, its number and the latest receipt/document number. With a closed shift the numbers describe the last closed shift.",
			DescriptionRU: "Входные параметры: нет. Состояние смены: 0 — смена закрыта, 1 — смена открыта. Если смена закрыта, номер смены — номер последней закрытой смены, иначе номер текущей смены. Если смена закрыта, номер чека — число документов предыдущей закрытой смены (0 для первой смены). Если смена открыта и чеков еще нет, возвращается 0, иначе номер последнего сформированного чека.",
			Input:         []ParameterDefinition{}, Output: []ParameterDefinition{
				{Name: "shiftState", TitleRU: "Состояние смены", Description: "0 closed; 1 open.", JSONType: "integer", WireType: "BYTE", Required: true, Enum: map[string]string{"0": "Closed", "1": "Open"}},
				{Name: "shiftNumber", TitleRU: "Номер смены", Description: "Current open shift, or the last closed shift when closed.", JSONType: "integer", WireType: "UINT16", Required: true},
				{Name: "receiptNumber", TitleRU: "Номер чека", Description: "Last receipt/document number, with the zero and closed-shift rules described in the command notes.", JSONType: "integer", WireType: "UINT16", Required: true},
			},
		}),
		makeCommand(commandSpec{
			Code: 0x21, Name: "startOpenShift", TitleRU: "Начать открытие смены", Category: "shift", Page: 14,
			Description:   "Begin opening a shift. The optional suppressPrint byte controls whether the opening report is printed; omission means printing.",
			DescriptionRU: "Формировать отчет без вывода на печать: 0 — отчет об открытии смены будет напечатан; 1 — отчет об открытии смены НЕ БУДЕТ напечатан. Параметр необязательный; если не передается, документ выводится на печать. Условия выполнения: ФН должен быть в фискальном режиме. Время открытия смены может на 1 час отставать от времени закрытия предыдущей смены (или времени фискализации ФН). Выходные данные: нет.",
			Input:         []ParameterDefinition{registrationSuppressPrint()}, Output: []ParameterDefinition{},
			Preconditions: []string{"The FN must be in fiscal mode.", "The shift-opening time may lag the previous shift-closing or fiscalization time by at most one hour, as stated by the protocol."},
		}),
		makeCommand(commandSpec{
			Code: 0x22, Name: "completeOpenShift", TitleRU: "Открыть смену", Category: "shift", Page: 15,
			Description:   "Complete shift opening after 0x21 and, when required, cashier details supplied with 0x2F. Returns the opened shift number and fiscal document identifiers.",
			DescriptionRU: "Команда, завершающая процедуру открытия смены. Только после выполнения данной команды становятся возможными процедуры формирования чеков и закрытия смены. Условия выполнения: должна быть выполнена команда «Начать открытие смены», а также, если ККТ не работает в автоматическом режиме, должны быть переданы данные кассира (0x2F). Входные параметры: нет. Выходные данные: номер открытой смены UINT16 LE, номер ФД UINT32 LE, фискальный признак UINT32 LE.",
			Input:         []ParameterDefinition{}, Output: registrationShiftFiscalResult(),
			Preconditions: []string{"0x21 must have completed successfully.", "Cashier details 0x2F are required unless the KKT operates automatically."},
		}),
		makeCommand(commandSpec{
			Code: 0x29, Name: "startCloseShift", TitleRU: "Начать закрытие смены", Category: "shift", Page: 15,
			Description:   "Begin closing the shift. The optional suppressPrint byte controls printing of the closing report; omission means printing.",
			DescriptionRU: "Формировать отчет без вывода на печать: 0 — отчет о закрытии смены будет напечатан; 1 — отчет о закрытии смены НЕ БУДЕТ напечатан. Параметр необязательный; если не передается, документ выводится на печать. Условия выполнения в руководстве: ФН должен быть в фискальном режиме; время открытия смены может на 1 час отставать от времени закрытия предыдущей смены (или времени фискализации ФН). Выходные данные: нет.",
			Input:         []ParameterDefinition{registrationSuppressPrint()}, Output: []ParameterDefinition{},
			Preconditions: []string{"The FN must be in fiscal mode; see the manual's additional time-condition wording on page 15."},
		}),
		makeCommand(commandSpec{
			Code: 0x2A, Name: "completeCloseShift", TitleRU: "Закрыть смену", Category: "shift", Page: 15,
			Description:   "Complete shift closing after 0x29 and any required cashier details. Returns the closed shift number and fiscal document identifiers.",
			DescriptionRU: "Команда завершает процедуру закрытия смены. Условия выполнения: должна быть выполнена команда «Начать закрытие смены», а также, если ККТ не работает в автоматическом режиме, должны быть переданы данные кассира (0x2F). Входные параметры: нет. Выходные данные: номер закрытой смены UINT16 LE, номер ФД UINT32 LE, фискальный признак UINT32 LE.",
			Input:         []ParameterDefinition{}, Output: registrationShiftFiscalResult(),
			Preconditions: []string{"0x29 must have completed successfully.", "Cashier details 0x2F are required unless the KKT operates automatically."},
		}),
		makeCommand(commandSpec{
			Code: 0x2F, Name: "sendCashierData", TitleRU: "Передать данные кассира", Category: "shift", Page: 14, RequestLayout: "tlv",
			Description:   "Supply cashier name and optional twelve-digit INN. This command is optional during shift opening/closing for automatic self-service KKT operation.",
			DescriptionRU: "Входные параметры — TLV: ФИО кассира (тэг 1021, ASCII, не более 64 символов); ИНН кассира (тэг 1203, ASCII), параметр необязательный, если передается — строго 12 символов. Выходные данные: нет. Для ККТ в составе автоматических устройств самообслуживания выполнение данной команды в процессе открытия-закрытия смены необязательно.",
			Input: []ParameterDefinition{
				{Name: "cashierName", TitleRU: "ФИО Кассира", Description: "Cashier's full name, at most 64 CP866 bytes.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Required: true, Tag: 1021, MinLength: 1, MaxLength: 64},
				{Name: "cashierInn", TitleRU: "ИНН Кассира", Description: "Optional cashier INN; if present, exactly twelve decimal digits.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 1203, FixedLength: 12},
			},
			Output:   []ParameterDefinition{},
			Validate: func(params map[string]json.RawMessage) error { return registrationDigits(params, "cashierInn", 12) },
		}),
	}
}

func registrationFiscalResult() []ParameterDefinition {
	return []ParameterDefinition{
		{Name: "documentNumber", TitleRU: "Номер ФД", Description: "Fiscal document number.", JSONType: "integer", WireType: "UINT32", Required: true},
		{Name: "fiscalSign", TitleRU: "Фискальный признак", Description: "Fiscal document sign.", JSONType: "integer", WireType: "UINT32", Required: true},
	}
}

func registrationShiftFiscalResult() []ParameterDefinition {
	return append([]ParameterDefinition{{Name: "shiftNumber", TitleRU: "Номер смены", Description: "Number of the opened or closed shift.", JSONType: "integer", WireType: "UINT16", Required: true}}, registrationFiscalResult()...)
}

func registrationSuppressPrint() ParameterDefinition {
	return ParameterDefinition{Name: "suppressPrint", TitleRU: "Формировать отчет без вывода на печать", Description: "Optional byte: 0 print the report; 1 suppress printing. Omission means printing and remains an omitted wire byte.", JSONType: "integer", WireType: "BYTE", Enum: map[string]string{"0": "Print report", "1": "Do not print report"}, Notes: "The protocol's default on omission is printing; no byte is inserted automatically."}
}

func registrationDataValidate(params map[string]json.RawMessage) error {
	if err := registrationDigits(params, "representativeInn", 12); err != nil {
		return err
	}
	if err := registrationDigits(params, "ofdInn", 10, 12); err != nil {
		return err
	}
	var operatingMode uint8
	if err := json.Unmarshal(params["operatingMode"], &operatingMode); err != nil {
		return fmt.Errorf("operatingMode must be an unsigned byte")
	}
	if operatingMode&0x02 == 0 {
		for _, name := range []string{"ofdInn", "ofdName", "senderEmail"} {
			if _, ok := params[name]; !ok {
				return fmt.Errorf("%s is required outside autonomous mode", name)
			}
		}
	}
	return nil
}

func registrationDigits(params map[string]json.RawMessage, name string, lengths ...int) error {
	raw, present := params[name]
	if !present {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return fmt.Errorf("%s must be a decimal string", name)
	}
	validLength := false
	for _, length := range lengths {
		validLength = validLength || len(text) == length
	}
	if !validLength {
		return fmt.Errorf("%s must contain %v decimal digits", name, lengths)
	}
	for _, digit := range text {
		if digit < '0' || digit > '9' {
			return fmt.Errorf("%s must contain decimal digits only", name)
		}
	}
	return nil
}

func registrationAgentBits() map[string]string {
	return map[string]string{"0": "Bank payment agent", "1": "Bank payment subagent", "2": "Payment agent", "3": "Payment subagent", "4": "Attorney", "5": "Commission agent", "6": "Other agent"}
}

func registrationModeBits() map[string]string {
	return map[string]string{"0": "Encryption", "1": "Autonomous mode", "2": "Automatic mode", "3": "Services", "4": "Automated strict-reporting forms", "5": "Internet electronic payments"}
}

func registrationTaxationBits() map[string]string {
	return map[string]string{"0": "General taxation", "1": "Simplified income", "2": "Simplified income minus expenses", "3": "Imputed income (as listed in this protocol revision)", "4": "Unified agricultural tax", "5": "Patent taxation"}
}

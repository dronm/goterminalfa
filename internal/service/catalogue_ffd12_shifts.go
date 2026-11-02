package service

import (
	"encoding/json"
	"fmt"
)

// ffd12ShiftCommands returns replacement/additional definitions for the FFD 1.2
// catalogue. It does not modify base. In particular, 0x22/0x2A have an extra
// warning byte in this supplement; their base-profile decoders remain unchanged.
func ffd12ShiftCommands(base []Command) []Command {
	commands := []Command{
		ffd12ShiftCommand(commandSpec{
			Code: 0x20, Name: "getShiftParameters", TitleRU: "Запрос параметров текущей смены", Category: "shift", Page: 9, ReadOnly: true,
			Description:   "Read the current shift state, shift number and last receipt/document number. The FFD 1.2 supplement retains the five-byte positional layout of this query.",
			DescriptionRU: "Запрос параметров текущей смены (0x20). Входные параметры: нет. Выходные данные: состояние смены, BYTE, 0 — смена закрыта, 1 — смена открыта; номер смены, UINT16 LE, если смена закрыта — номер последней закрытой смены, если открыта — номер текущей смены; номер чека, UINT16 LE, если смена закрыта — число документов в предыдущей закрытой смене (0, если это первая смена); если смена открыта, но нет ни одного чека — 0; в остальных случаях — номер последнего сформированного чека.",
			Output: []ParameterDefinition{
				{Name: "shiftState", TitleRU: "Состояние смены", Description: "0: shift closed; 1: shift open. Unknown firmware values are preserved.", JSONType: "integer", WireType: "BYTE", Required: true, Enum: map[string]string{"0": "смена закрыта", "1": "смена открыта"}},
				{Name: "shiftNumber", TitleRU: "Номер смены", Description: "Current shift number when open, otherwise the last closed shift number.", JSONType: "integer", WireType: "UINT16", Required: true},
				{Name: "receiptNumber", TitleRU: "Номер чека", Description: "Last receipt number, or the previous closed shift's document count; zero cases follow the command description.", JSONType: "integer", WireType: "UINT16", Required: true},
			},
		}),
		ffd12ShiftCommand(commandSpec{
			Code: 0x21, Name: "startOpenShift", TitleRU: "Начать открытие смены", Category: "shift", Page: 9,
			Description:   "Begin opening an FFD 1.2 shift. Optional suppressPrint=0 prints the report; 1 suppresses printing; omission also means printing without inserting a wire byte. In data-transfer mode this command periodically updates marking-code verification keys and may take approximately fifteen seconds.",
			DescriptionRU: "Начать открытие смены (0x21). Входной параметр «Формировать отчет без вывода на печать», BYTE: 0 — отчет об открытии смены будет напечатан; 1 — отчет об открытии смены не будет напечатан. Параметр необязательный; если не передается, документ выводится на печать. Выходные данные: нет. Условия выполнения: ФН должен быть в фискальном режиме. Время открытия смены может на 1 час отставать от времени закрытия предыдущей смены или времени фискализации ФН. Для ККТ в режиме передачи данных в рамках данной команды выполняется периодическое обновление ключей проверки кодов маркировки, что может занять порядка 15 секунд.",
			Input:         []ParameterDefinition{registrationSuppressPrint()},
			Notes:         []string{"Allow for approximately 15 seconds of marking-key updates when selecting the exchange and complete-operation timeouts.", "0xE4 is the optional shift-opening data command described by the FFD 1.2 supplement."},
			Preconditions: []string{"The FN must be in fiscal mode.", "The documented opening-time tolerance is at most one hour behind the previous shift-closing or fiscalization time.", "Finish this shift-opening operation with 0x22 under the same device lock."},
		}),
		ffd12ShiftCommand(commandSpec{
			Code: 0x22, Name: "completeOpenShift", TitleRU: "Открыть смену", Category: "shift", Page: 10,
			Description:   "Complete FFD 1.2 shift opening after 0x21 and any optional 0xE4 data. Returns shift number, fiscal document number, fiscal sign and one warning-flags byte: eleven bytes in total.",
			DescriptionRU: "Открыть смену (0x22). Входные параметры: нет. Выходные данные: номер открытой смены, UINT16 LE; номер ФД, UINT32 LE; фискальный признак, UINT32 LE; флаги предупреждений, BYTE, битовая маска таблицы 1. Команда завершает процедуру открытия смены. Только после ее выполнения становятся возможными процедуры формирования чеков и закрытия смены. Предварительно должна быть выполнена команда «Начать открытие смены».",
			Output:        ffd12ShiftFiscalResult(),
			Notes:         []string{"The FFD 1.2 response requires its eleventh warning byte. A ten-byte base-profile response is not silently accepted."},
			Preconditions: []string{"0x21 must have completed successfully.", "Any supplied shift-opening data 0xE4 must precede this command in the same operation."},
		}),
		ffd12ShiftCommand(commandSpec{
			Code: 0x29, Name: "startCloseShift", TitleRU: "Начать закрытие смены", Category: "shift", Page: 10,
			Description:   "Begin closing an FFD 1.2 shift. The optional suppressPrint byte retains the documented printing default: omission or 0 prints, 1 suppresses printing.",
			DescriptionRU: "Начать закрытие смены (0x29). Входной параметр «Формировать отчет без вывода на печать», BYTE: 0 — отчет о закрытии смены будет напечатан; 1 — отчет о закрытии смены не будет напечатан. Параметр необязательный; если не передается, документ выводится на печать. Выходные данные: нет. Условия выполнения в дополнении: ФН должен быть в фискальном режиме; время открытия смены может на 1 час отставать от времени закрытия предыдущей смены или времени фискализации ФН.",
			Input:         []ParameterDefinition{registrationSuppressPrint()},
			Notes:         []string{"0xE5 is the optional shift-closing data command described by the FFD 1.2 supplement."},
			Preconditions: []string{"The FN must be in fiscal mode; the supplement repeats the opening-time tolerance wording for this command.", "Finish this shift-closing operation with 0x2A under the same device lock."},
		}),
		ffd12ShiftCommand(commandSpec{
			Code: 0x2A, Name: "completeCloseShift", TitleRU: "Закрыть смену", Category: "shift", Page: 11,
			Description:   "Complete FFD 1.2 shift closing after 0x29 and any optional 0xE5 data. Returns shift number, fiscal document number, fiscal sign and the warning-flags byte: eleven bytes in total.",
			DescriptionRU: "Закрыть смену (0x2A). Входные параметры: нет. Выходные данные: номер закрытой смены, UINT16 LE; номер ФД, UINT32 LE; фискальный признак, UINT32 LE; флаги предупреждений, BYTE, битовая маска таблицы 1 для открытия смены. Команда завершает процедуру закрытия смены. Предварительно должна быть выполнена команда «Начать закрытие смены».",
			Output:        ffd12ShiftFiscalResult(),
			Notes:         []string{"The FFD 1.2 response requires its eleventh warning byte. The base profile continues using its own ten-byte layout."},
			Preconditions: []string{"0x29 must have completed successfully.", "Any supplied shift-closing data 0xE5 must precede this command in the same operation."},
		}),
		ffd12ShiftDataCommand(0xE4, "sendShiftOpeningData", "Передать данные открытия смены", 9, 1276, 1277, "0x21", "0x22"),
		ffd12ShiftDataCommand(0xE5, "sendShiftClosingData", "Передать данные закрытия смены", 10, 1278, 1279, "0x29", "0x2A"),
	}

	// 0x2F is requested for compatibility but is not redefined by the supplement.
	// Retain its base validator and source instead of attributing an invented
	// FFD-specific layout to the new document. Prefer E4/E5 for their richer data.
	for _, candidates := range [][]Command{base, registrationCommands()} {
		for _, command := range candidates {
			if command.Definition.ProtocolCode != "0x2F" {
				continue
			}
			command.Definition.Notes = append(append([]string{}, command.Definition.Notes...), "Compatibility command from base protocol 01.05; the FFD 1.2 supplement does not redefine 0x2F. Its documented shift data commands are 0xE4 and 0xE5.")
			command.Definition.OperationRequired = true
			return append(commands, command)
		}
	}
	return commands
}

func ffd12ShiftCommand(spec commandSpec) Command {
	command := makeCommand(spec)
	command.Definition.Source = Source{
		Document: "Протокол информационного обмена ККТ Терминал-ФА: дополнение для работы с ФФД версии 1.2",
		Version:  "FFD 1.2", Page: spec.Page,
	}
	command.Definition.OperationRequired = !spec.ReadOnly
	return command
}

func ffd12ShiftFiscalResult() []ParameterDefinition {
	return append(registrationShiftFiscalResult(), ParameterDefinition{
		Name: "warningFlags", TitleRU: "Флаги предупреждений", Description: "Warning bitmask from table 1 of the FFD 1.2 supplement.",
		JSONType: "integer", WireType: "BYTE", Required: true,
		Bits: map[string]string{
			"0": "РЕСУРС ФН МЕНЕЕ 3 ДНЕЙ", "1": "РЕСУРС ФН МЕНЕЕ 30 ДНЕЙ",
			"2": "ПАМЯТЬ ФН ЗАПОЛНЕНА", "3": "ОФД НЕ ОТВЕЧАЕТ",
			"4": "ОШИБКА ФЛК", "5": "ТРЕБ. НАСТР. ККТ",
			"6": "ОФД АННУЛИРОВАН", "7": "КРИТИЧЕСКАЯ ОШИБКА ФН",
		},
	})
}

func ffd12ShiftDataCommand(code byte, name, title string, page int, attributeTag, dataTag uint16, begin, finish string) Command {
	return ffd12ShiftCommand(commandSpec{
		Code: code, Name: name, TitleRU: title, Category: "shift", Page: page, RequestLayout: "tlv",
		Description:   fmt.Sprintf("Optionally supply FFD 1.2 shift report data between %s and %s. The supplied command includes cashier name and document address/place; cashier INN is optional. reportAttribute and reportDataHex must be present together or both omitted.", begin, finish),
		DescriptionRU: fmt.Sprintf("%s (0x%02X). Входные параметры — TLV: ФИО кассира, тег 1021, ASCII(N), не более 64 символов; ИНН кассира, тег 1203, ASCII(N), необязательный, если передается — строго 12 символов; адрес формирования ФД, тег 1009, ASCII(N), не более 256 символов; место формирования ФД, тег 1187, ASCII(N), не более 256 символов; дополнительный реквизит отчета, тег %d, ASCII(N), не более 32 символов, необязательно; дополнительные данные отчета, тег %d, BYTE(N), не более 32 байт, необязательно. Выходные данные: нет. Выполнение команды не является обязательным в процессе открытия или закрытия смены: выполняется при необходимости передать соответствующие данные. При включении дополнительных тегов одновременно указываются оба значения.", title, code, attributeTag, dataTag),
		Input: []ParameterDefinition{
			{Name: "cashierName", TitleRU: "ФИО Кассира", Description: "Cashier's name, at most 64 encoded CP866 bytes; required when this command is supplied.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Required: true, Tag: 1021, MaxLength: 64},
			{Name: "cashierInn", TitleRU: "ИНН Кассира", Description: "Optional cashier INN; if supplied, exactly twelve decimal digits.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 1203, FixedLength: 12},
			{Name: "documentAddress", TitleRU: "Адрес формирования ФД", Description: "Fiscal document formation address, at most 256 encoded CP866 bytes; required when this command is supplied.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Required: true, Tag: 1009, MaxLength: 256},
			{Name: "documentPlace", TitleRU: "Место формирования ФД", Description: "Fiscal document formation place, at most 256 encoded CP866 bytes; required when this command is supplied.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Required: true, Tag: 1187, MaxLength: 256},
			{Name: "reportAttribute", TitleRU: "Дополнительный реквизит отчета", Description: "Optional additional report attribute; include reportDataHex at the same time.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: attributeTag, MaxLength: 32, RequiredIf: "Required when reportDataHex is present."},
			{Name: "reportDataHex", TitleRU: "Дополнительные данные отчета", Description: "Optional binary report data as hexadecimal text, at most 32 decoded bytes; include reportAttribute at the same time.", JSONType: "string", WireType: "HEX", Tag: dataTag, MaxLength: 32, RequiredIf: "Required when reportAttribute is present."},
		},
		Preconditions: []string{begin + " must have completed successfully in the same complete shift operation.", "This command is optional; if used, finish with " + finish + " under the same device lock."},
		Notes:         []string{"The table marks cashier name and document address/place as required within this optional command. It specifies maximum lengths but no positive minimum lengths for those fields.", "Binary lengths apply after hexadecimal decoding; text lengths apply after CP866 encoding."},
		Validate: func(params map[string]json.RawMessage) error {
			if err := registrationDigits(params, "cashierInn", 12); err != nil {
				return err
			}
			_, attribute := params["reportAttribute"]
			_, data := params["reportDataHex"]
			if attribute != data {
				return fmt.Errorf("reportAttribute and reportDataHex must be supplied together (tags %d and %d)", attributeTag, dataTag)
			}
			return nil
		},
	})
}

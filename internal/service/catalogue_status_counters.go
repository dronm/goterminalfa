package service

// statusCounterCommands covers the remaining status and cash-counter commands
// from the base protocol. Cash amounts retain their exact unsigned integer
// representation in kopecks; no floating-point conversion is involved.
func statusCounterCommands() []Command {
	return []Command{
		makeCommand(commandSpec{
			Code: 0x01, Name: "getStatus", TitleRU: "Запрос статуса ККТ", Category: "diagnostics", Page: 10, ReadOnly: true,
			Description:   "Read KKT status: the factory serial number, current device-local date/time, critical-error flag, printer status, fiscal storage presence and fiscal storage lifecycle flags. The positional response contains exactly 21 bytes.",
			DescriptionRU: "Запрос статуса ККТ (0x01). Входные параметры: нет. Выходные данные: заводской номер ККТ, ASCII(12); текущие дата/время в ККТ, DATETIME(5); критические ошибки в ККТ, BYTE(1), 0 — ошибок нет, 1 — присутствуют; статус печатающего устройства, BYTE(1), 0 — корректный статус, бумага присутствует, 1 — устройство не подключено, 2 — отсутствует бумага, 3 — замятие бумаги, 5 — открыта крышка ПУ, 6 — ошибка отрезчика ПУ, 7 — аппаратная ошибка ПУ; наличие ФН в ККТ, BYTE(1), 1 — ФН подключен, 0 — ФН не подключен; фаза жизни ФН, BYTE(1), значения битов см. приложение 7.",
			Notes:         []string{"Device-local calendar values have no implied timezone.", "Documented status dictionaries describe known values; unknown response values are preserved for newer firmware."},
			Output: []ParameterDefinition{
				{Name: "serialNumber", TitleRU: "Заводской номер ККТ", Description: "KKT factory serial number, distinct from the USB descriptor serial.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", FixedLength: 12, Required: true},
				{Name: "dateTime", TitleRU: "Текущие дата/время в ККТ", Description: "Current device-local calendar date/time, encoded as year since 2000, month, day, hour and minute.", JSONType: "string", WireType: "DATETIME5", Required: true},
				{Name: "criticalErrors", TitleRU: "Критические ошибки в ККТ", Description: "0: no critical errors; 1: critical errors present.", JSONType: "integer", WireType: "BYTE", Required: true, Enum: map[string]string{"0": "ошибок нет", "1": "присутствуют"}},
				{Name: "printerStatus", TitleRU: "Статус печатающего устройства", Description: "Printer state reported by the KKT.", JSONType: "integer", WireType: "BYTE", Required: true, Enum: map[string]string{"0": "корректный статус, бумага присутствует", "1": "устройство не подключено", "2": "отсутствует бумага", "3": "замятие бумаги", "5": "открыта крышка ПУ", "6": "ошибка отрезчика ПУ", "7": "аппаратная ошибка ПУ"}},
				{Name: "fiscalStoragePresent", TitleRU: "Наличие ФН в ККТ", Description: "0: fiscal storage absent; 1: fiscal storage connected.", JSONType: "integer", WireType: "BYTE", Required: true, Enum: map[string]string{"0": "ФН не подключен", "1": "ФН подключен"}},
				{Name: "lifecycleFlags", TitleRU: "Фаза жизни ФН", Description: "Fiscal storage lifecycle bits from Appendix 7. Documented combined phases are 1, 3, 7 and 15.", JSONType: "integer", WireType: "BYTE", Required: true, Bits: map[string]string{"0": "проведена настройка ФН", "1": "открыт фискальный режим", "2": "постфискальный режим", "3": "закончена передача ФД в ОФД"}},
			},
		}),
		makeCommand(commandSpec{
			Code: 0xBB, Name: "getPrintLineWidth", TitleRU: "Запрос количества символов в печатаемой строке", Category: "diagnostics", Page: 12, ReadOnly: true,
			Description:   "Read the configured number of characters per printed line. The KKT determines the value from the selected or detected printing device and font size.",
			DescriptionRU: "Запрос количества символов в печатаемой строке (настройки) (0xBB). Входные параметры: нет. Выходные данные: количество символов в строке, BYTE. Количество символов возвращается в соответствии с заданным (определенным) печатающим устройством и размером шрифта.",
			Output:        []ParameterDefinition{{Name: "charactersPerLine", TitleRU: "Количество символов в строке", Description: "Characters per printed line for the current printing device and font size.", JSONType: "integer", WireType: "BYTE", Required: true}},
		}),
		makeCommand(commandSpec{
			Code: 0xB0, Name: "getCashCounters", TitleRU: "Запрос суммы наличных и электронных в кассе", Category: "cashCounters", Page: 27, ReadOnly: true,
			Description:   "Read the cash and electronic-payment counters since shift opening. Each amount is an exact five-byte little-endian unsigned integer in kopecks. The KKT adds sale and expense-refund receipt amounts plus cash deposits, and subtracts expense and sale-refund receipt amounts plus cash withdrawals for the corresponding payment type.",
			DescriptionRU: "Запрос суммы наличных и электронных в кассе (0xB0). Возвращает суммы денежных средств наличными и электронными с момента открытия смены. Для соответствующего типа оплаты суммируются все значения по кассовым чекам и чекам коррекции с признаками расчетов «приход» и «возврат расхода», а также все внесения денежных средств; вычитаются все значения по кассовым чекам и чекам коррекции по «расход» и «возврат прихода», а также все выплаты денежных средств. Входные параметры: нет. Выходные данные: сумма наличными в кассе, UINT40 LE, в копейках; сумма электронными, проведенная через ККТ, UINT40 LE, в копейках.",
			Output: []ParameterDefinition{
				{Name: "cashKopecks", TitleRU: "Сумма наличными в кассе", Description: "Cash counter since the shift opened, in integer kopecks.", JSONType: "integer", WireType: "UINT40", Required: true, Unit: "kopecks"},
				{Name: "electronicKopecks", TitleRU: "Сумма электронными, проведенная через ККТ", Description: "Electronic-payment counter since the shift opened, in integer kopecks.", JSONType: "integer", WireType: "UINT40", Required: true, Unit: "kopecks"},
			},
		}),
		makeCommand(commandSpec{
			Code: 0xB1, Name: "resetCashCounters", TitleRU: "Сброс счетчиков денежных средств", Category: "cashCounters", Page: 27,
			Description:   "Reset the monetary counters retained by the KKT since the shift opened. This changes device state and requires a persisted requestId.",
			DescriptionRU: "Сброс счетчиков денежных средств (0xB1). Выполняет обнуление денежных счетчиков, сохраненных в ККТ с начала смены. Входные параметры: нет. Выходные данные: нет.",
		}),
		makeCommand(commandSpec{
			Code: 0xB2, Name: "printCashCountersReport", TitleRU: "Печать информационного отчета", Category: "cashCounters", Page: 27,
			Description:   "Generate and print an informational report of the cash-register monetary operations since the shift opened. Printing is a side effect, so a persisted requestId is required.",
			DescriptionRU: "Печать информационного отчета (0xB2). Формирует и выводит на печать информационный отчет обо всех денежных операциях, проведенных с начала смены по кассе. Входные параметры: нет. Выходные данные: нет.",
		}),
		makeCommand(commandSpec{
			Code: 0xB3, Name: "depositCash", TitleRU: "Внесение наличных денежных средств", Category: "cashCounters", Page: 27,
			Description:   "Record a cash deposit. Supply amountKopecks as an unsigned decimal integer or decimal-digit string; the driver encodes the exact amount as a minimal-length little-endian VLN. No response data is documented.",
			DescriptionRU: "Внесение наличных денежных средств (0xB3). Входные параметры: сумма внесения наличными, VLN, в копейках. Выходные данные: нет.",
			Input:         []ParameterDefinition{cashAmountParameter("Сумма внесения наличными", "Cash deposit amount in integer kopecks.")},
		}),
		makeCommand(commandSpec{
			Code: 0xB4, Name: "withdrawCash", TitleRU: "Выплата наличных денежных средств", Category: "cashCounters", Page: 27,
			Description:   "Record a cash withdrawal. Supply amountKopecks as an unsigned decimal integer or decimal-digit string; the driver encodes the exact amount as a minimal-length little-endian VLN. No response data is documented.",
			DescriptionRU: "Выплата наличных денежных средств (0xB4). Входные параметры: сумма выплаты наличными, VLN, в копейках. Выходные данные: нет.",
			Input:         []ParameterDefinition{cashAmountParameter("Сумма выплаты наличными", "Cash withdrawal amount in integer kopecks.")},
		}),
	}
}

func cashAmountParameter(title, description string) ParameterDefinition {
	return ParameterDefinition{
		Name: "amountKopecks", TitleRU: title, Description: description,
		JSONType: "integer", WireType: "VLN", Required: true, Unit: "kopecks",
		Notes: "An unsigned decimal-digit JSON string is also accepted to preserve values above the exact-integer range of floating-point JSON clients. Zero is accepted; the protocol table does not specify a positive-only minimum.",
	}
}

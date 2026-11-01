package service

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// archivePrintCommands covers archive access, printer operations, test-FN reset,
// and the clock commands that are fully described in protocol version 01.05.
func archivePrintCommands() []Command {
	documentNumber := ParameterDefinition{Name: "documentNumber", TitleRU: "Номер фискального документа", Description: "Fiscal document number in the fiscal storage archive.", JSONType: "integer", WireType: "UINT32", Required: true, Min: limit(1)}
	registrationNumber := ParameterDefinition{Name: "registrationReportNumber", TitleRU: "Порядковый номер отчета о регистрации", Description: "Ordinal number of the registration or re-registration report.", JSONType: "integer", WireType: "BYTE", Required: true}
	dateTime := ParameterDefinition{Name: "dateTime", TitleRU: "Дата и время", Description: "Device-local calendar date and time, without a timezone conversion.", JSONType: "string", WireType: "DATETIME5", Required: true, Tag: 30000}
	printerOutput := []ParameterDefinition{
		{Name: "printerStatus", TitleRU: "Статус печатающего устройства", Description: "Zero means the printer is ready; a nonzero status reports why the requested printer action could not be performed.", JSONType: "integer", WireType: "BYTE", Enum: printerStatusNames(), Notes: "The status byte is optional in a successful protocol response; an empty payload denotes status zero."},
	}
	font := ParameterDefinition{Name: "fontAttributes", TitleRU: "Атрибуты шрифта", Description: "Printer font attribute bitmask.", JSONType: "integer", WireType: "BYTE", Required: true, Bits: map[string]string{"3": "Expanded mode", "4": "Double height", "5": "Double width", "6": "Italic", "7": "Underline"}, Notes: "Bit values: 0x08 expanded, 0x10 height, 0x20 width, 0x40 italic, 0x80 underline."}
	alignment := ParameterDefinition{Name: "alignment", TitleRU: "Выравнивание", Description: "Text alignment.", JSONType: "integer", WireType: "BYTE", Required: true, Enum: map[string]string{"0": "Left", "1": "Center", "2": "Right"}}
	lineFields := []ParameterDefinition{font, alignment, {Name: "text", TitleRU: "Текст", Description: "Text encoded as CP866 for the printer.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Required: true}}
	bufferLineFields := append([]ParameterDefinition(nil), lineFields...)
	bufferLineFields[2].MaxLength = 124

	optionalRegistrationNumber := registrationNumber
	optionalRegistrationNumber.Required = false
	optionalRegistrationNumber.Notes = "If omitted, the first registration report is returned and the response has no re-registration reason byte. Omission is different from an explicit zero."
	registrationOutput := activationResultFields(true)
	registrationOutput[5].Required = false
	registrationOutput[5].Notes = "Present only in the 48-byte response; absent in the 47-byte initial-registration response."

	commands := []Command{
		makeCommand(commandSpec{
			Code: 0x30, Name: "findFiscalDocument", TitleRU: "Найти фискальный документ по номеру", Category: "archive", ReadOnly: true, Page: 23,
			Description:   "Read one archived fiscal document by number. The document payload is preserved as hexadecimal because its structure depends on the returned document type.",
			DescriptionRU: "Команда позволяет найти в Архиве ФН фискальный документ по его номеру. Документ выдается в ответном сообщении. Формат структуры данных зависит от типа документа и описан в приложении 5.",
			Input:         []ParameterDefinition{documentNumber},
			Output: []ParameterDefinition{
				{Name: "documentType", TitleRU: "Тип документа", Description: "Document type from Appendix 5; retained without assuming the distinct TLV form-code dictionary.", JSONType: "integer", WireType: "BYTE", Required: true},
				{Name: "ofdAcknowledged", TitleRU: "Получена ли квитанция из ОФД", Description: "1 if an OFD receipt exists, otherwise 0.", JSONType: "integer", WireType: "BYTE", Required: true, Enum: map[string]string{"0": "No", "1": "Yes"}},
				{Name: "documentData", TitleRU: "Данные фискального документа", Description: "Exact archived document bytes as hexadecimal; Appendix 5 defines type-specific field layouts.", JSONType: "string", WireType: "RAW", Required: true},
			},
			Notes: []string{"Archive document payloads retain their exact bytes. The TLV form codes in Appendix 6 are a separate dictionary and are not assumed to identify the byte returned by this command."},
		}),
		makeCommand(commandSpec{
			Code: 0x31, Name: "printOfdAcknowledgement", TitleRU: "Печать Подтверждения Оператора по номеру ФД", Category: "archive", Page: 23,
			Description:   "Print the operator acknowledgement for an archived fiscal document, if present. The response includes its date/time, 18-byte fiscal sign, and document number.",
			DescriptionRU: "Команда позволяет напечатать документ «Подтверждение оператора» в случае наличия данного подтверждения.",
			Input:         []ParameterDefinition{documentNumber}, Output: []ParameterDefinition{
				{Name: "acknowledgedAt", TitleRU: "Дата и время подтверждения", Description: "OFD acknowledgement date and time.", JSONType: "string", WireType: "DATETIME5", Required: true},
				{Name: "ofdFiscalSign", TitleRU: "Фискальный признак оператора", Description: "Exact 18-byte operator fiscal sign as hexadecimal.", JSONType: "string", WireType: "HEX", FixedLength: 18, Required: true},
				{Name: "documentNumber", TitleRU: "Номер ФД", Description: "Acknowledged fiscal document number.", JSONType: "integer", WireType: "UINT32", Required: true},
			},
		}),
		makeCommand(commandSpec{
			Code: 0x32, Name: "getUnconfirmedDocumentCount", TitleRU: "Запрос количества ФД без подтверждения ОФД", Category: "archive", ReadOnly: true, Page: 23,
			Description: "Read the number of fiscal documents not yet acknowledged by the OFD.", DescriptionRU: "Запрос количества фискальных документов без подтверждения ОФД. Входные параметры: нет. Выходные данные: количество неподтвержденных ФД, UINT16 LE.",
			Output: []ParameterDefinition{{Name: "count", TitleRU: "Количество неподтвержденных ФД", Description: "Number of unacknowledged fiscal documents.", JSONType: "integer", WireType: "UINT16", Required: true}},
		}),
		makeCommand(commandSpec{
			Code: 0x33, Name: "getActivationTotals", TitleRU: "Запрос итогов активации ФН", Category: "archive", ReadOnly: true, Page: 24,
			Description:   "Read the initial or a selected registration report. The payload is 47 bytes when the optional report index is omitted and 48 bytes when supplied.",
			DescriptionRU: "Если в команду не передается параметр «Порядковый номер отчета о регистрации», то возвращаются данные первичного отчета о регистрации ККТ. Также в выходных данных будет отсутствовать код причины перерегистрации, длина ответа равна 47 байт. Если параметр указан, возвращаются данные соответствующего отчета о регистрации или перерегистрации, длина ответа равна 48 байт.",
			Input:         []ParameterDefinition{optionalRegistrationNumber}, Output: registrationOutput, Decode: decodeActivationTotals,
		}),
		makeCommand(commandSpec{
			Code: 0x35, Name: "selectFiscalDocumentTLV", TitleRU: "Запрос фискального документа в TLV формате", Category: "archive", Page: 23,
			Description:   "Select an archived fiscal document and initialize the TLV read cursor. Follow with readFiscalDocumentTLV until all advertised bytes have been consumed.",
			DescriptionRU: "Запрос фискального документа в TLV формате. Входные параметры: номер ФД, UINT32 LE. Выходные данные: тип ФД, UINT16 LE, и длина ФД, UINT16 LE. Тип ФД соответствует коду формы документа, см. приложение 6.",
			Input:         []ParameterDefinition{documentNumber}, Output: []ParameterDefinition{
				{Name: "documentType", TitleRU: "Тип ФД", Description: "Fiscal document form code from Appendix 6.", JSONType: "integer", WireType: "UINT16", Required: true},
				{Name: "documentLength", TitleRU: "Длина ФД", Description: "Total document length in bytes.", JSONType: "integer", WireType: "UINT16", Required: true},
			}, Notes: []string{"This command changes the device archive cursor and requires idempotency even though it does not create a fiscal document. Select and subsequent reads must remain in one protected operation."},
		}),
		makeCommand(commandSpec{
			Code: 0x36, Name: "readFiscalDocumentTLV", TitleRU: "Чтение TLV фискального документа", Category: "archive", Page: 23,
			Description:   "Read the next TLV structure of the previously selected fiscal document. The operation advances the device cursor; it must not be blindly repeated after a lost response.",
			DescriptionRU: "Команда считывает очередную TLV структуру документа, предварительно запрошенного по номеру, до тех пор пока не будут считаны все данные документа. Условия выполнения: должна быть выполнена команда «Запрос фискального документа в TLV формате».",
			Output:        archiveTLVOutput(), Decode: decodeArchiveTLV, Preconditions: []string{"A fiscal document must first be selected with command 0x35; read operations must preserve ownership of that document cursor."},
			Notes: []string{"Repeating a completed requestId replays the saved response without advancing the cursor again. Use a new requestId for each intentional next chunk."},
		}),
		makeCommand(commandSpec{
			Code: 0x39, Name: "printFiscalDocument", TitleRU: "Печать ФД по номеру", Category: "archive", Page: 23,
			Description: "Print a copy of an archived fiscal document by number. Any extra successful response bytes are ignored as instructed by the protocol.", DescriptionRU: "Команда позволяет напечатать копию ФД по его номеру. Если от ККТ возвращаются какие-либо данные, за исключением кода ответа, их следует игнорировать.",
			Input: []ParameterDefinition{documentNumber}, Decode: func(_ []byte) (any, error) { return map[string]any{}, nil },
		}),
		makeCommand(commandSpec{
			Code: 0x3A, Name: "readFiscalDocumentTags", TitleRU: "Чтение любого фискального документа всем тэгам", Category: "archive", ReadOnly: true, Page: 14,
			Description: "Read all TLV or STLV objects of a fiscal document. Tag order, repeated tags, and unrecognized values are retained.", DescriptionRU: "Чтение любого фискального документа всем тэгам. Входные параметры: номер ФД, UINT32. Выходные данные: массив TLV или STLV объектов, содержащихся в документе. Размер документа не должен превышать 1024 байт.",
			Input: []ParameterDefinition{documentNumber}, Output: archiveTLVOutput(), Decode: decodeArchiveTLV,
		}),
		makeCommand(commandSpec{
			Code: 0x3B, Name: "readRegistrationReportTags", TitleRU: "Чтение отчета о регистрации по всем тэгам", Category: "archive", ReadOnly: true, Page: 14,
			Description: "Read all TLV objects of a selected registration report. Unknown and repeated tags remain available as hexadecimal values.", DescriptionRU: "Чтение отчета о регистрации по всем тэгам. Входные параметры: порядковый номер отчета о регистрации или перерегистрации, BYTE. Выходные данные: массив TLV объектов, содержащихся в отчете. Размер документа не должен превышать 1024 байт.",
			Input: []ParameterDefinition{registrationNumber}, Output: archiveTLVOutput(), Decode: decodeArchiveTLV,
		}),
		makeCommand(commandSpec{
			Code: 0x40, Name: "resetTestFiscalStorage", TitleRU: "Сброс состояния МГМ", Category: "debug", Page: 28,
			Description: "Reset only a fiscal-storage mass/dimensional simulator running debug firmware. The request contains the fixed byte 0x16. A production fiscal storage will not execute it.", DescriptionRU: "Команда применяется для сброса состояния массогабаритного макета с отладочной версией прошивки. После успешного выполнения команды МГМ возвращается к фазе жизни «проведена Настройка». На рабочем ФН команда выполнена не будет. Код запроса задается жестко и должен быть равен 0x16.",
			Encode: encodeTestStorageReset, Notes: []string{"No caller-supplied reset byte is accepted; the documented fixed byte 0x16 is encoded by the driver."},
		}),
		makeCommand(commandSpec{
			Code: 0x61, Name: "printText", TitleRU: "Напечатать строку", Category: "printer", Page: 24,
			Description: "Print one CP866 text string with font attributes and alignment. A returned printer-status byte is reported explicitly.", DescriptionRU: "Напечатать строку символов. Передаются атрибуты шрифта, выравнивание и текст. Если статус печатающего устройства корректный, возвращаемых данных нет; иначе возвращается BYTE со статусом печатающего устройства, см. приложение 4.",
			Input: lineFields, Output: printerOutput, Decode: decodePrinterResult,
		}),
		makeCommand(commandSpec{
			Code: 0x62, Name: "cutPaper", TitleRU: "Отрезать бумагу", Category: "printer", Page: 24,
			Description: "Cut paper fully or partially. Omitting partialCut requests a full cut. Partial cutting depends on printer support.", DescriptionRU: "Частичный отрез: 0 — полный отрез, 1 — частичный отрез. Параметр необязательный; если не передается, выполняется полный отрез бумаги. Параметр актуален только для печатающих устройств, поддерживающих функцию частичного отреза. Возвращаемый BYTE содержит статус печатающего устройства, если печать невозможна.",
			Input: []ParameterDefinition{{Name: "partialCut", TitleRU: "Частичный отрез", Description: "Optional cut mode; omission requests a full cut.", JSONType: "integer", WireType: "BYTE", Enum: map[string]string{"0": "Full", "1": "Partial"}}}, Output: printerOutput, Decode: decodePrinterResult,
		}),
		makeCommand(commandSpec{
			Code: 0x63, Name: "printQRCode", TitleRU: "Печать QR-кода", Category: "printer", Page: 25,
			Description: "Print a QR code for a CP866 string containing at least six encoded characters.", DescriptionRU: "Печать QR-кода. Входные параметры: строка для кодирования, ASCII, длина не менее 6 символов. Если статус печатающего устройства не позволяет выполнить печать, ответ содержит BYTE со статусом печатающего устройства.",
			Input: []ParameterDefinition{{Name: "text", TitleRU: "Строка для кодирования", Description: "QR code content, at least six CP866 bytes.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Required: true, MinLength: 6}}, Output: printerOutput, Decode: decodePrinterResult,
		}),
		makeCommand(commandSpec{
			Code: 0x64, Name: "bufferFormattedText", TitleRU: "Записать в буфер печати массив форматированных строк символов", Category: "printer", Page: 26, RequestLayout: "tlv",
			Description:   "Append formatted text lines to the printer buffer. Each line is a repeated TLV tag 39999 whose value contains fontAttributes, alignment, and up to 124 CP866 text bytes.",
			DescriptionRU: "В каждом TLV значение T одинаково и равно 39999. Длина LEN равна длине строки плюс два байта: атрибуты шрифта и выравнивание. Отдельной командой можно передавать один или несколько текстовых фрагментов, не превышая максимальный размер пакета. Максимальная длина текста одной строки — 124 байта; максимальное количество строк в буфере — 255. Последующее выполнение 0x65 печатает буфер и очищает его. Если буфер не напечатан, он выводится в специальную область кассового чека; переданный в 0x24 дополнительный текст имеет приоритет над буфером.",
			Input:         []ParameterDefinition{{Name: "lines", TitleRU: "Форматированные строки", Description: "One or more formatted lines; each object becomes a separate tag 39999.", JSONType: "array", WireType: "STRUCT", Required: true, Tag: 39999, Repeated: true, MinItems: 1, MaxItems: 255, Children: bufferLineFields}},
			Notes:         []string{"The driver enforces the per-command packet bound, per-line 124-byte limit, and cumulative 255-line limit within an atomic operation. Preexisting buffer content written by another controller cannot be inferred.", "The inconsistent stray byte in the printed example is not copied; encoding follows its documented tag-length-value layout."},
		}),
		makeCommand(commandSpec{
			Code: 0x65, Name: "printTextBuffer", TitleRU: "Печать буфера форматированных строк", Category: "printer", Page: 26,
			Description: "Print all buffered formatted lines and clear the device buffer.", DescriptionRU: "Команда выводит на печатающее устройство все данные из буфера печати с заданным форматированием. После печати буфер очищается. Входные параметры: нет. Выходные данные: нет.",
		}),
		makeCommand(commandSpec{
			Code: 0x69, Name: "printBarcode", TitleRU: "Печать штрих-кода", Category: "printer", Page: 25,
			Description: "Print EAN13 or CODE128 text. EAN13 content is at most 13 CP866 bytes; CODE128 content is at most 24 bytes.", DescriptionRU: "Тип штрих-кода: 0 — EAN13, 1 — CODE128. Строка для кодирования: для EAN13 не более 13 символов, для CODE128 не более 24 символов. Если статус печатающего устройства не позволяет выполнить печать, ответ содержит BYTE со статусом печатающего устройства.",
			Input: []ParameterDefinition{
				{Name: "barcodeType", TitleRU: "Тип штрих-кода", Description: "0 for EAN13, 1 for CODE128.", JSONType: "integer", WireType: "BYTE", Required: true, Enum: map[string]string{"0": "EAN13", "1": "CODE128"}},
				{Name: "text", TitleRU: "Строка для кодирования", Description: "Barcode content; the maximum length depends on barcodeType.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Required: true, MaxLength: 24, Notes: "EAN13 maximum 13 bytes; CODE128 maximum 24 bytes."},
			}, Output: printerOutput, Validate: validateBarcodeLength, Decode: decodePrinterResult,
		}),
		makeCommand(commandSpec{
			Code: 0x72, Name: "setDateTime", TitleRU: "Установить дату/время в ККТ", Category: "settings", Page: 29, RequestLayout: "tlv",
			Description: "Set the device-local calendar date/time using TLV tag 30000 with a five-byte YMDHm value.", DescriptionRU: "Установить дату/время в ККТ. Входные параметры: дата/время, TLV, TAG=30000, LEN=5, тип DATETIME(5). Выходные данные: нет.",
			Input: []ParameterDefinition{dateTime},
		}),
		makeCommand(commandSpec{
			Code: 0x73, Name: "getDateTime", TitleRU: "Запросить дату/время из ККТ", Category: "settings", ReadOnly: true, Page: 29, ResponseLayout: "tlv",
			Description: "Read the device-local calendar date/time from TLV tag 30000. No timezone or daylight-saving conversion is applied.", DescriptionRU: "Запросить дату/время из ККТ. Входные параметры: нет. Выходные данные: текущие дата/время, TLV, TAG=30000, LEN=5, тип DATETIME(5).",
			Output: []ParameterDefinition{dateTime},
		}),
	}
	return commands
}

func activationResultFields(withReason bool) []ParameterDefinition {
	fields := []ParameterDefinition{
		{Name: "dateTime", TitleRU: "Дата и время", Description: "Registration report date and time.", JSONType: "string", WireType: "DATETIME5", Required: true},
		{Name: "taxpayerINN", TitleRU: "ИНН", Description: "Taxpayer INN, padded with spaces to twelve bytes on the wire.", JSONType: "string", WireType: "ASCII", FixedLength: 12, Padding: "space", Required: true},
		{Name: "registrationNumber", TitleRU: "РН ККТ", Description: "KKT registration number, padded to twenty bytes.", JSONType: "string", WireType: "ASCII", FixedLength: 20, Padding: "space", Required: true},
		{Name: "taxationSystems", TitleRU: "Код налогообложения", Description: "Registered taxation-system bitmask.", JSONType: "integer", WireType: "BYTE", Required: true},
		{Name: "operatingModes", TitleRU: "Режим работы", Description: "Registered operating-mode bitmask.", JSONType: "integer", WireType: "BYTE", Required: true},
	}
	if withReason {
		fields = append(fields, ParameterDefinition{Name: "reregistrationReason", TitleRU: "Код причины перерегистрации", Description: "Reason for re-registration.", JSONType: "integer", WireType: "BYTE", Required: true})
	}
	return append(fields,
		ParameterDefinition{Name: "documentNumber", TitleRU: "Номер ФД", Description: "Fiscal document number.", JSONType: "integer", WireType: "UINT32", Required: true},
		ParameterDefinition{Name: "fiscalSign", TitleRU: "Фискальный признак", Description: "Fiscal document sign.", JSONType: "integer", WireType: "UINT32", Required: true},
	)
}

func decodeActivationTotals(data []byte) (any, error) {
	if len(data) != 47 && len(data) != 48 {
		return nil, fmt.Errorf("activation totals must contain 47 or 48 bytes, received %d", len(data))
	}
	return decodeFields(activationResultFields(len(data) == 48), "positional", data)
}

func archiveTLVOutput() []ParameterDefinition {
	return []ParameterDefinition{
		{Name: "tlv", TitleRU: "Массив TLV объектов", Description: "Ordered TLV values; repeated and unknown tags are preserved as tag/valueHex objects. STLV values retain their exact nested payload bytes.", JSONType: "array", WireType: "TLV[]", Required: true},
		{Name: "rawHex", TitleRU: "Данные ответа", Description: "Exact complete TLV response bytes as hexadecimal, including all tag and length headers.", JSONType: "string", WireType: "RAW", Required: true},
	}
}

func decodeArchiveTLV(data []byte) (any, error) {
	objects, err := ParseTLV(data)
	if err != nil {
		return nil, err
	}
	values := make([]map[string]any, 0, len(objects))
	for _, object := range objects {
		values = append(values, map[string]any{"tag": object.Tag, "valueHex": hex.EncodeToString(object.Value)})
	}
	return map[string]any{"tlv": values, "rawHex": hex.EncodeToString(data)}, nil
}

func encodeTestStorageReset(raw json.RawMessage) ([]byte, error) {
	params, err := decodeParams(raw)
	if err != nil {
		return nil, err
	}
	if len(params) != 0 {
		return nil, fmt.Errorf("resetTestFiscalStorage accepts no parameters")
	}
	return []byte{0x16}, nil
}

func printerStatusNames() map[string]string {
	return map[string]string{"0": "Ready", "1": "Printer not connected", "2": "Out of paper", "3": "Paper jam", "5": "Cover open", "6": "Cutter error", "7": "Hardware error"}
}

func decodePrinterResult(data []byte) (any, error) {
	if len(data) > 1 {
		return nil, fmt.Errorf("printer response must be empty or contain one status byte, received %d", len(data))
	}
	status := byte(0)
	if len(data) == 1 {
		status = data[0]
	}
	description, known := printerStatusNames()[fmt.Sprintf("%d", status)]
	if !known {
		description = "Unknown printer status"
	}
	return map[string]any{"printerStatus": uint64(status), "printerStatusDescription": description, "printerReady": status == 0}, nil
}

func validateBarcodeLength(params map[string]json.RawMessage) error {
	var barcodeType uint64
	var text string
	if err := json.Unmarshal(params["barcodeType"], &barcodeType); err != nil {
		return fmt.Errorf("barcodeType must be an integer")
	}
	if err := json.Unmarshal(params["text"], &text); err != nil {
		return fmt.Errorf("text must be a string")
	}
	maxLength := 24
	if barcodeType == 0 {
		maxLength = 13
	}
	// CP866 is a single-byte encoding. Generic encoding rejects characters that
	// cannot be represented, so valid text has one encoded byte per rune.
	if len([]rune(text)) > maxLength {
		return fmt.Errorf("barcode text exceeds %d encoded characters for the selected barcodeType", maxLength)
	}
	return nil
}

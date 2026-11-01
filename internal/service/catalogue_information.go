package service

import (
	"fmt"

	terminalfa "github.com/dronm/goterminalfa"
)

// informationCommands contains the non-mutating identification and diagnostic
// commands from protocol pages 10–12. In particular, 0x09 is intentionally left
// opaque: the manual specifies BYTE(N), not a record layout for that buffer.
func informationCommands() []Command {
	serial := makeCommand(commandSpec{
		Code: 0x02, Name: "getSerialNumber", TitleRU: "Запрос заводского номера ККТ", Category: "identification", Page: 10, ReadOnly: true,
		Description:   "Read the KKT factory serial number. No input parameters. The successful response contains exactly twelve decimal characters. This is not the USB descriptor serial number.",
		DescriptionRU: "Запрос заводского номера ККТ (0x02). Входные параметры: нет. Выходные данные: заводской номер ККТ, ASCII(12).",
		Output:        []ParameterDefinition{{Name: "serialNumber", TitleRU: "Заводской номер ККТ", Description: "KKT factory serial number, distinct from the USB descriptor serial.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", FixedLength: 12, Required: true}},
		Decode: func(data []byte) (any, error) {
			if len(data) != 12 {
				return nil, &terminalfa.InvalidSerialError{Message: fmt.Sprintf("expected 12 bytes, received %d", len(data))}
			}
			for _, b := range data {
				if b < '0' || b > '9' {
					return nil, &terminalfa.InvalidSerialError{Message: "factory serial contains non-decimal bytes"}
				}
			}
			return SerialNumberResult{SerialNumber: string(data)}, nil
		},
	})
	serial.Definition.Aliases = append(serial.Definition.Aliases, "ID")

	return []Command{
		serial,
		makeCommand(commandSpec{
			Code: 0x03, Name: "getFirmwareVersion", TitleRU: "Запрос версии ПО ККТ", Category: "identification", Page: 10, ReadOnly: true,
			Description:   "Read the KKT firmware version as a CP866 string with the length supplied by the response frame.",
			DescriptionRU: "Запрос версии ПО ККТ (0x03). Входные параметры: нет. Выходные данные: версия ПО ККТ, ASCII(N).",
			Output:        []ParameterDefinition{informationText("firmwareVersion", "Версия ПО ККТ", "KKT firmware version.")},
		}),
		makeCommand(commandSpec{
			Code: 0x04, Name: "getModel", TitleRU: "Запрос модели ККТ", Category: "identification", Page: 10, ReadOnly: true,
			Description:   "Read the KKT model name as a CP866 string with the length supplied by the response frame.",
			DescriptionRU: "Запрос модели ККТ (0x04). Входные параметры: нет. Выходные данные: название модели ККТ, ASCII(N).",
			Output:        []ParameterDefinition{informationText("model", "Название модели ККТ", "KKT model name.")},
		}),
		makeCommand(commandSpec{
			Code: 0x05, Name: "getFiscalStorageSerialNumber", TitleRU: "Запрос заводского номера ФН", Category: "identification", Page: 10, ReadOnly: true,
			Description:   "Read the factory serial number of the fiscal storage unit. The wire field is variable-length ASCII(N), as specified by this command's table.",
			DescriptionRU: "Запрос заводского номера ФН (0x05). Входные параметры: нет. Выходные данные: заводской номер ФН, ASCII(N).",
			Output:        []ParameterDefinition{informationText("fiscalStorageSerialNumber", "Заводской номер ФН", "Factory serial number of the fiscal storage unit.")},
		}),
		makeCommand(commandSpec{
			Code: 0x06, Name: "getFiscalStorageFirmwareVersion", TitleRU: "Запрос версии ПО ФН", Category: "identification", Page: 10, ReadOnly: true,
			Description:   "Read the fiscal storage firmware version as a variable-length CP866 string.",
			DescriptionRU: "Запрос версии ПО ФН (0x06). Входные параметры: нет. Выходные данные: версия ПО ФН, ASCII(N).",
			Output:        []ParameterDefinition{informationText("firmwareVersion", "Версия ПО ФН", "Fiscal storage firmware version.")},
		}),
		makeCommand(commandSpec{
			Code: 0x07, Name: "getFiscalStorageValidity", TitleRU: "Запрос срока действия ФН", Category: "diagnostics", Page: 10, ReadOnly: true,
			Description:   "Read the fiscal storage expiration date and the counts of available and completed re-registrations. The three-byte date is year since 2000, month, day.",
			DescriptionRU: "Запрос срока действия ФН (0x07). Входные параметры: нет. Выходные данные: срок действия ФН, BYTE(3), YMD (год начиная с 2000); доступно перерегистраций, BYTE(1), целое; проведено перерегистраций, BYTE(1), целое.",
			Output: []ParameterDefinition{
				{Name: "expiresOn", TitleRU: "Срок действия ФН", Description: "Expiration date; year is encoded relative to 2000.", JSONType: "string", WireType: "DATETIME3", Required: true},
				informationByte("availableReregistrations", "Доступно перерегистраций", "Number of available re-registrations."),
				informationByte("completedReregistrations", "Проведено перерегистраций", "Number of completed re-registrations."),
			},
		}),
		makeCommand(commandSpec{
			Code: 0x08, Name: "getFiscalStorageStatus", TitleRU: "Запрос статуса ФН", Category: "diagnostics", Page: 11, ReadOnly: true,
			Description:   "Read fiscal storage lifecycle flags, open document, received-data flag, shift state, warnings, last document date/time, fiscal storage serial number, and last fiscal document number.",
			DescriptionRU: "Запрос статуса ФН (0x08). Входные параметры: нет. Выходные данные: фаза жизни ФН, BYTE; текущий документ, BYTE; данные документа, BYTE (0 — нет данных документа, 1 — получены данные документа); состояние смены, BYTE (0 — смена закрыта, 1 — смена открыта); флаги и предупреждения, BYTE (приложение 3); дата и время последнего документа, DATETIME(5), YMDHm; номер ФН, ASCII(16); номер последнего ФД, UINT32 LE.",
			Notes:         []string{"The positional response is exactly 30 bytes.", "A zero-filled last-document date is returned as null; no timezone is invented for device calendar values."},
			Output: []ParameterDefinition{
				{Name: "lifecycleFlags", TitleRU: "Фаза жизни ФН", Description: "Fiscal storage lifecycle bitmask; see Appendix 7.", JSONType: "integer", WireType: "BYTE", Required: true, Bits: map[string]string{"0": "проведена настройка ФН", "1": "открыт фискальный режим", "2": "постфискальный режим", "3": "закончена передача ФД в ОФД"}},
				{Name: "currentDocument", TitleRU: "Текущий документ", Description: "Code of the currently open fiscal document.", JSONType: "integer", WireType: "BYTE", Required: true, Enum: map[string]string{"0": "нет открытого документа", "1": "отчет о регистрации ККТ", "2": "отчет об открытии смены", "4": "кассовый чек", "8": "отчет о закрытии смены", "16": "отчет о закрытии фискального режима", "18": "отчет об изменении параметров регистрации в связи с заменой ФН", "19": "отчет об изменении параметров регистрации ККТ", "20": "кассовый чек коррекции", "23": "отчет о текущем состоянии расчетов"}},
				{Name: "documentDataReceived", TitleRU: "Данные документа", Description: "0: document data absent; 1: document data received.", JSONType: "integer", WireType: "BYTE", Required: true, Enum: map[string]string{"0": "нет данных документа", "1": "получены данные документа"}},
				{Name: "shiftOpen", TitleRU: "Состояние смены", Description: "0: shift closed; 1: shift open.", JSONType: "integer", WireType: "BYTE", Required: true, Enum: map[string]string{"0": "смена закрыта", "1": "смена открыта"}},
				{Name: "warningFlags", TitleRU: "Флаги и предупреждения", Description: "Fiscal storage warning flags from Appendix 3.", JSONType: "integer", WireType: "BYTE", Required: true, Bits: map[string]string{"0": "срочная замена КС: до окончания срока действия 3 дня", "1": "исчерпание ресурса КС: до окончания срока действия 30 дней", "2": "переполнение памяти ФН: архив заполнен на 90%", "3": "превышено время ожидания ответа ОФД", "7": "критическая ошибка ФН"}},
				{Name: "lastDocumentDateTime", TitleRU: "Дата и время последнего документа", Description: "Device-local calendar date/time of the last document, or null for a zero-filled date.", JSONType: "string", WireType: "DATETIME5", Required: true, AllowZeroDate: true},
				{Name: "fiscalStorageSerialNumber", TitleRU: "Номер ФН", Description: "Fixed-width fiscal storage serial number.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", FixedLength: 16, Required: true},
				{Name: "lastDocumentNumber", TitleRU: "Номер последнего ФД", Description: "Last fiscal document number.", JSONType: "integer", WireType: "UINT32", Required: true},
			},
		}),
		makeCommand(commandSpec{
			Code: 0x09, Name: "getFiscalStorageErrors", TitleRU: "Запрос последних ошибок ФН", Category: "diagnostics", Page: 12, ReadOnly: true,
			Description:   "Read the last fiscal storage errors as an opaque hexadecimal byte buffer. The manual does not define record boundaries or fields inside BYTE(N).",
			DescriptionRU: "Запрос последних ошибок ФН (0x09). Входные параметры: нет. Выходные данные: буфер данных, BYTE(N).",
			Notes:         []string{"No undocumented interpretation of the error buffer is performed."},
			Output:        []ParameterDefinition{{Name: "dataHex", TitleRU: "Буфер данных", Description: "Uninterpreted error buffer represented as hexadecimal, preserving every byte.", JSONType: "string", WireType: "RAW", Required: true}},
		}),
		makeCommand(commandSpec{
			Code: 0x0A, Name: "getRegistrationParameters", TitleRU: "Запрос текущих параметров регистрации ККТ", Category: "diagnostics", Page: 11, ReadOnly: true,
			Description:   "Read parameters of the most recent KKT registration or re-registration. Registration number and taxpayer INN are right-padded with spaces on the wire; output strings omit that padding.",
			DescriptionRU: "Запрос текущих параметров регистрации ККТ (0x0A). Команда возвращает данные последней регистрации (перерегистрации) ККТ. Входные параметры: нет. Выходные данные: РН ККТ, ASCII(20), дополняется пробелами справа до длины 20 символов; ИНН, ASCII(12), дополняется пробелами справа до длины 12 символов; режимы работы ККТ, BYTE, битовая маска; режимы налогообложения, BYTE, битовая маска; признак платежного агента, BYTE, битовая маска. Значения битов см. приложение 7.",
			Output: []ParameterDefinition{
				{Name: "registrationNumber", TitleRU: "РН ККТ", Description: "KKT registration number, excluding right-space padding.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", FixedLength: 20, Padding: "space", Required: true},
				{Name: "taxpayerINN", TitleRU: "ИНН", Description: "Taxpayer INN, excluding right-space padding.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", FixedLength: 12, Padding: "space", Required: true},
				{Name: "operatingModes", TitleRU: "Режимы работы ККТ", Description: "Operating-mode bitmask from Appendix 7.", JSONType: "integer", WireType: "BYTE", Required: true, Bits: map[string]string{"0": "шифрование", "1": "автономный режим", "2": "автоматический режим", "3": "применение в сфере услуг", "4": "режим БСО; иначе режим кассовых чеков", "5": "ККТ для интернет"}},
				{Name: "taxationSystems", TitleRU: "Режимы налогообложения", Description: "Taxation-system bitmask from Appendix 7; the dictionary reproduces the protocol's terminology.", JSONType: "integer", WireType: "BYTE", Required: true, Bits: map[string]string{"0": "общая", "1": "упрощенная доход", "2": "упрощенная доход минус расход", "3": "единый налог на вмененный доход", "4": "единый сельскохозяйственный налог", "5": "патентная система налогообложения"}},
				{Name: "paymentAgentFlags", TitleRU: "Признак платежного агента", Description: "Agent-role bitmask from Appendix 7.", JSONType: "integer", WireType: "BYTE", Required: true, Bits: map[string]string{"0": "банковский платежный агент", "1": "банковский платежный субагент", "2": "платежный агент", "3": "платежный субагент", "4": "поверенный", "5": "комиссионер", "6": "агент, не являющийся банковским платежным агентом, платежным агентом, поверенным или комиссионером"}},
			},
		}),
		makeCommand(commandSpec{
			Code: 0x0B, Name: "getConfigurationVersion", TitleRU: "Запрос версии конфигурации ККТ", Category: "identification", Page: 11, ReadOnly: true,
			Description:   "Read the KKT configuration version, documented as an X.X.X string.",
			DescriptionRU: "Запрос версии конфигурации ККТ (0x0B). Входные параметры: нет. Выходные данные: номер версии конфигурации ККТ, ASCII(N), строка в формате X.X.X.",
			Output:        []ParameterDefinition{informationText("configurationVersion", "Номер версии конфигурации ККТ", "Configuration version, documented as X.X.X.")},
		}),
		makeCommand(commandSpec{
			Code: 0x0E, Name: "getNetworkParameters", TitleRU: "Запрос текущих параметров TCP/IP Ethernet интерфейса", Category: "diagnostics", Page: 12, ReadOnly: true,
			Description:   "Read the current Ethernet IPv4 address, subnet mask and default gateway. Each field is a little-endian UINT32 whose numeric value is interpreted in conventional IPv4 octet order.",
			DescriptionRU: "Запрос текущих параметров TCP/IP Ethernet интерфейса (0x0E). Входные параметры: нет. Выходные данные: IP адрес, UINT32 LE; маска подсети, UINT32 LE; шлюз по умолчанию, UINT32 LE. Например, IP-адрес 0xC0A80A3A означает 192.168.10.58; маска 0xFFFFFF00 означает 255.255.255.0; шлюз 0xC0A80A01 означает 192.168.10.1.",
			Output: []ParameterDefinition{
				{Name: "ipAddress", TitleRU: "IP адрес", Description: "Current Ethernet IPv4 address.", JSONType: "string", WireType: "IPV4", Required: true},
				{Name: "subnetMask", TitleRU: "Маска подсети", Description: "IPv4 subnet mask.", JSONType: "string", WireType: "IPV4", Required: true},
				{Name: "defaultGateway", TitleRU: "Шлюз по умолчанию", Description: "IPv4 default gateway.", JSONType: "string", WireType: "IPV4", Required: true},
			},
		}),
		makeCommand(commandSpec{
			Code: 0x50, Name: "getOFDExchangeStatus", TitleRU: "Запрос статуса информационного обмена с ОФД", Category: "diagnostics", Page: 12, ReadOnly: true,
			Description:   "Read OFD exchange state, pending-message count, first queued fiscal document number and its device-local date/time. Two leading status bytes are service values whose meanings are not expanded by the manual.",
			DescriptionRU: "Запрос статуса информационного обмена с ОФД (0x50). Входные параметры: нет. Выходные данные: статус информационного обмена, BYTE, служебный параметр; состояние чтения сообщения для ОФД, BYTE, служебный параметр; количество сообщений для передачи в ОФД, UINT16 LE; номер документа для ОФД первого в очереди, UINT32 LE; дата-время документа для ОФД первого в очереди, DATETIME YMDHm (5 байт).",
			Output: []ParameterDefinition{
				informationByte("exchangeStatus", "Статус информационного обмена", "Service status value; the manual does not define its enumeration."),
				informationByte("messageReadState", "Состояние чтения сообщения для ОФД", "Service message-read state; the manual does not define its enumeration."),
				{Name: "pendingMessages", TitleRU: "Количество сообщений для передачи в ОФД", Description: "Number of messages awaiting OFD transmission.", JSONType: "integer", WireType: "UINT16", Required: true},
				{Name: "firstDocumentNumber", TitleRU: "Номер документа для ОФД первого в очереди", Description: "Fiscal document number at the head of the OFD queue.", JSONType: "integer", WireType: "UINT32", Required: true},
				{Name: "firstDocumentDateTime", TitleRU: "Дата-время документа для ОФД первого в очереди", Description: "Device-local date/time of the first queued document, or null for a zero-filled date.", JSONType: "string", WireType: "DATETIME5", Required: true, AllowZeroDate: true},
			},
		}),
	}
}

func informationText(name, title, description string) ParameterDefinition {
	return ParameterDefinition{Name: name, TitleRU: title, Description: description, JSONType: "string", WireType: "ASCII", Encoding: "CP866", Required: true}
}

func informationByte(name, title, description string) ParameterDefinition {
	return ParameterDefinition{Name: name, TitleRU: title, Description: description, JSONType: "integer", WireType: "BYTE", Required: true}
}

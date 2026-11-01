package service

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
)

var receiptVATRates = map[string]string{
	"1": "НДС 20%", "2": "НДС 10%", "3": "НДС расч. 20/120", "4": "НДС расч. 10/110",
	"5": "НДС 0%", "6": "Без НДС", "7": "НДС 5%", "8": "НДС 7%",
	"9": "НДС расч. 5/105", "10": "НДС расч. 7/107", "11": "НДС 22%", "12": "НДС расч. 22/122",
}

var receiptCalculationSigns = map[string]string{
	"1": "Приход", "2": "Возврат прихода", "3": "Расход", "4": "Возврат расхода",
}

var receiptTaxSystems = map[string]string{
	"1": "Общая", "2": "УСН доход", "4": "УСН доход минус расход", "8": "ЕНВД",
	"16": "ЕСХН", "32": "Патент",
}

var receiptAgentSigns = map[string]string{
	"0": "Не является агентом", "1": "Банковский платежный агент", "2": "Банковский платежный субагент",
	"4": "Платежный агент", "8": "Платежный субагент", "16": "Поверенный", "32": "Комиссионер", "64": "Агент",
}

func receiptParam(name, title, wire string, tag uint16, required bool, maxLength int) ParameterDefinition {
	apiType := "integer"
	if wire == "ASCII" || wire == "FVLN" || wire == "DATETIME3" || wire == "DATETIME5" {
		apiType = "string"
	}
	if wire == "STLV" {
		apiType = "object"
	}
	p := ParameterDefinition{Name: name, TitleRU: title, Description: title, JSONType: apiType,
		WireType: wire, Tag: tag, Required: required, MaxLength: maxLength}
	if wire == "ASCII" {
		p.Encoding = "CP866"
	}
	if wire == "VLN" || wire == "UINT40" {
		p.Unit = "kopecks"
	}
	return p
}

func receiptResultFields() []ParameterDefinition {
	return []ParameterDefinition{
		receiptParam("receiptNumber", "Номер чека за смену", "UINT16", 0, true, 0),
		receiptParam("documentNumber", "Номер ФД", "UINT32", 0, true, 0),
		receiptParam("fiscalSign", "Фискальный признак", "UINT32", 0, true, 0),
	}
}

func receiptCommands() []Command {
	resultFields := receiptResultFields()
	modernResultFields := append(append([]ParameterDefinition{}, resultFields...),
		receiptParam("dateTime", "Дата-время формирования чека", "DATETIME5", 0, false, 0),
		receiptParam("shiftNumber", "Номер смены", "UINT16", 0, false, 0))
	sign := receiptParam("calculationSign", "Признак расчета", "BYTE", 0, true, 0)
	sign.Enum = receiptCalculationSigns
	total := receiptParam("totalKopecks", "Итог чека в копейках", "UINT40", 0, true, 0)
	additionalText := receiptParam("additionalText", "Дополнительная информация для печати на чеке", "ASCII", 0, false, 512)

	commands := []Command{
		makeCommand(commandSpec{
			Code: 0x19, Name: "addCashlessPaymentDetails", TitleRU: "Передать сведения об оплате безналичными", Category: "receipts", Page: 19,
			Description:   "Supply details of one cashless payment after payment data has been supplied. The KKT forms STLV 1234–1235 from the listed input TLVs. This command is not valid for correction receipts.",
			DescriptionRU: "Данная команда формирует STLV 1234–1235 (сведения обо всех оплатах по чеку безналичными). Может быть выполнена после передачи данных оплаты (0x2D). Для кассовых чеков коррекции команда недопустима и вернет ошибку 0x28. Дополнительные сведения о безналичной оплате необязательны. Справочник способа оплаты в документе не актуализирован ФНС.",
			RequestLayout: "tlv", Input: []ParameterDefinition{
				receiptParam("amountKopecks", "Сумма оплаты безналичными", "VLN", 1082, true, 0),
				receiptParam("paymentMethod", "Признак способа оплаты безналичными", "BYTE", 1236, true, 0),
				receiptParam("identifiers", "Идентификаторы безналичной оплаты", "ASCII", 1237, true, 256),
				receiptParam("additionalDetails", "Дополнительные сведения о безналичной оплате", "ASCII", 1238, false, 256),
			}, Preconditions: []string{"A normal receipt is open and command 0x2D has succeeded; not valid for correction receipts."},
		}),
		makeCommand(commandSpec{
			Code: 0x1F, Name: "setReceiptDeviceDetails", TitleRU: "Передать данные автоматического устройства расчетов для кассового чека", Category: "receipts", Page: 19,
			Description:   "Supply the settlement address, place and automatic device number for a receipt when those details were omitted at registration. All three input tags are required by this command.",
			DescriptionRU: "Одна ККТ может применяться в составе нескольких автоматических устройств. Если при регистрации не указывались адрес расчетов, место расчетов или номер автомата, сведения передаются при формировании чека командой 0x1F. Таблица команды требует все три тега. Если при регистрации отсутствовало только место расчета и тег 1187 передан в данных оплаты, выполнять 0x1F не требуется.",
			RequestLayout: "tlv", Input: receiptDeviceFields(true),
			Preconditions: []string{"A normal receipt is open; apply the registration-dependent requirements in the manual."},
		}),
		makeCommand(commandSpec{
			Code: 0x23, Name: "beginReceipt", TitleRU: "Открыть кассовый чек", Category: "receipts", Page: 16,
			Description:   "Begin a normal receipt. The shift must be open, no other receipt may be open, and the KKT time may not exceed the shift opening time by more than 24 hours.",
			DescriptionRU: "Смена должна быть открыта и не был начат другой чек. Дата и время не должны превосходить более чем на 24 часа дату и время открытия данной смены. Входные параметры и выходные данные отсутствуют.",
			Preconditions: []string{"Shift is open; no other receipt is open; shift age is at most 24 hours."},
		}),
		makeCommand(commandSpec{
			Code: 0x24, Name: "finalizeReceipt", TitleRU: "Сформировать чек", Category: "receipts", Page: 19,
			Description:   "Finalize a normal receipt using its calculation sign, total in kopecks and optional extra printed text. Response layouts of 10, 15 and 17 bytes are supported as documented by the firmware change log.",
			DescriptionRU: "Команда завершает процедуру формирования чека. Должен быть открыт чек и переданы все необходимые данные. Итог чека упаковывается в 5 байт UINT40 LE. Дополнительная информация для печати необязательна, не более 512 символов. С версии 1.0.18 ответ содержит дату-время, с версии 1.0.19 — номер смены. Если передан дополнительный текст, он выводится вместо буфера форматированных строк.",
			Input:         []ParameterDefinition{sign, total, additionalText}, Output: modernResultFields,
			Preconditions: []string{"Normal receipt is open and all required item/payment data has been supplied."},
			Decode: func(data []byte) (any, error) {
				count := 0
				switch len(data) {
				case 10:
					count = 3
				case 15:
					count = 4
				case 17:
					count = 5
				default:
					return nil, fmt.Errorf("finalizeReceipt response must contain 10, 15 or 17 bytes, got %d", len(data))
				}
				return decodeFields(modernResultFields[:count], "positional", data)
			},
		}),
		makeCommand(commandSpec{
			Code: 0x25, Name: "beginCorrectionReceipt", TitleRU: "Открыть кассовый чек коррекции", Category: "correction", Page: 20,
			Description:   "Begin a correction receipt. The shift must be open, no other receipt may be open, and the KKT time may not exceed the shift opening time by more than 24 hours.",
			DescriptionRU: "Смена должна быть открыта и не был начат другой чек. Дата и время не должны превосходить более чем на 24 часа дату и время открытия данной смены. Входные параметры и выходные данные отсутствуют.",
			Preconditions: []string{"Shift is open; no other receipt is open; shift age is at most 24 hours."},
		}),
		makeCommand(commandSpec{
			Code: 0x26, Name: "finalizeCorrectionReceipt", TitleRU: "Сформировать чек коррекции", Category: "correction", Page: 21,
			Description:   "Finalize a correction receipt. Default encoding follows firmware 1.0.18 and newer: calculation sign followed directly by a VLN total. Supply legacyVatRate only when explicitly targeting older firmware.",
			DescriptionRU: "Команда завершает процедуру формирования чека коррекции после открытия и передачи данных коррекции. Начиная с версии конфигурации 1.0.18 параметр ставки НДС в команде 0x26 не передается; все данные о суммах НДС передаются командой 0x2E. Для старых версий ставка НДС передается между признаком расчета и суммой.",
			Input:         receiptCorrectionFinalizeFields(), Output: resultFields,
			Preconditions: []string{"Correction receipt is open and command 0x2E has succeeded."},
			Notes:         []string{"legacyVatRate is an explicit compatibility option for firmware before 1.0.18; the driver does not guess firmware versions. When present it is inserted on the wire between calculationSign and totalKopecks."},
			Encode: func(raw json.RawMessage) ([]byte, error) {
				params, err := decodeParams(raw)
				if err != nil {
					return nil, err
				}
				fields := receiptCorrectionFinalizeFields()
				if _, present := params["legacyVatRate"]; present {
					fields = []ParameterDefinition{fields[0], fields[2], fields[1]}
				} else {
					fields = fields[:2]
				}
				return encodeFields(fields, "positional", raw)
			},
		}),
		makeCommand(commandSpec{
			Code: 0x27, Name: "beginSettlementReport", TitleRU: "Начать формирование отчета о состоянии расчетов", Category: "reports", Page: 22,
			Description:   "Begin a fiscal report on the settlement state. The fiscal storage must be in fiscal or post-fiscal mode.",
			DescriptionRU: "Команда начинает процедуру формирования фискального документа «Отчет о состоянии расчетов». ФН должен быть в состоянии «Фискальный режим» или «Постфискальный режим». Входные параметры и выходные данные отсутствуют.",
			Preconditions: []string{"Fiscal storage is in fiscal or post-fiscal mode."},
		}),
		makeCommand(commandSpec{
			Code: 0x28, Name: "finalizeSettlementReport", TitleRU: "Сформировать отчет о состоянии расчетов", Category: "reports", Page: 22,
			Description:   "Complete the previously started settlement-state report and return its fiscal identifiers and unconfirmed-document information.",
			DescriptionRU: "Команда завершает процедуру формирования отчета о состоянии расчетов. Предварительно должна быть выполнена команда «Начать формирование отчета о состоянии расчетов». Ответ содержит номер ФД, фискальный признак, количество неподтвержденных документов UINT32 LE и дату первого неподтвержденного документа YMD.",
			Preconditions: []string{"Command 0x27 has succeeded."},
			Output: []ParameterDefinition{
				receiptParam("documentNumber", "Номер ФД", "UINT32", 0, true, 0),
				receiptParam("fiscalSign", "Фискальный признак", "UINT32", 0, true, 0),
				receiptParam("unconfirmedCount", "Количество неподтвержденных документов", "UINT32", 0, true, 0),
				{Name: "firstUnconfirmedDate", TitleRU: "Дата первого неподтвержденного документа", Description: "Date of the first unconfirmed document; null when all date bytes are zero.", JSONType: "string", WireType: "DATETIME3", Required: true, AllowZeroDate: true},
			},
		}),
		makeCommand(commandSpec{
			Code: 0x2B, Name: "addReceiptItem", TitleRU: "Передать данные предмета расчета", Category: "receipts", Page: 16,
			Description:   "Supply one receipt item as STLV tag 1059. The price is in integer kopecks and quantity is an exact decimal string. VAT values include the additions in appendix 10, including 22% and calculated 22/122.",
			DescriptionRU: "Для передачи предметов расчета необходимо предварительное успешное открытие кассового чека. Каждый предмет расчета передается отдельной командой 0x2B. Предмет расчета — STLV 1059, содержащий перечисленные TLV. Если пользователь не является агентом, тег 1222 можно не передавать либо передать равным нулю, остальные данные агента и поставщика не передаются. При одинаковых данных агента по всем предметам они могут передаваться отдельно командой 0x2C. Код товарной номенклатуры передается строковым HEX-представлением, за его формирование отвечает ПО верхнего уровня. Для признаков предмета расчета 15 и 16 наименование выбирается из приложения 7. Ставки НДС дополнены приложением 10: значения 1–12.",
			RequestLayout: "tlv", Input: []ParameterDefinition{{Name: "item", TitleRU: "Предмет расчета", Description: "Receipt item containing nested TLV fields.", JSONType: "object", WireType: "STLV", Tag: 1059, Required: true, Children: receiptItemFields()}},
			Preconditions: []string{"Normal receipt is open; exactly one item is supplied per command."},
			Validate:      validateReceiptItem,
		}),
		makeCommand(commandSpec{
			Code: 0x2C, Name: "setReceiptAgentData", TitleRU: "Передать данные платежного агента", Category: "receipts", Page: 18,
			Description:   "Supply shared payment-agent details for a receipt. agentSign selects one registered agent role or zero; when zero, no other agent fields may be supplied.",
			DescriptionRU: "Команда выполняется только для ККТ, зарегистрированной с признаком платежного агента, после открытия кассового чека. Тег 1057 может принимать одно из значений, указанных при регистрации, либо 0, если пользователь в данном чеке не выступает агентом; при 0 остальные данные не передаются. Если данные агента и поставщика уже переданы по предметам расчета, выполнять 0x2C не нужно. Обязательность остальных полей зависит от типа агента по таблице документации.",
			RequestLayout: "tlv", Input: receiptSharedAgentFields(), Validate: validateReceiptSharedAgent,
			Preconditions: []string{"Normal receipt is open; KKT registration permits the selected agent role; item-specific agent data has not already been supplied."},
		}),
		makeCommand(commandSpec{
			Code: 0x2D, Name: "setReceiptPayment", TitleRU: "Передать данные оплаты", Category: "receipts", Page: 18,
			Description:   "Supply taxation, explicit totals for all payment methods, and optional cashier/customer attributes. Amounts are in integer kopecks. internetPayment=1 requires settlementPlace and customerContact.",
			DescriptionRU: "Для передачи данных оплаты необходимо предварительное успешное открытие кассового чека. Применяемая система налогообложения должна быть одним из значений, указанных при регистрации. Суммы наличными, безналичными, предоплатой, постоплатой и встречным предоставлением передаются в копейках. Данные кассира в автоматическом режиме могут не указываться. При признаке расчета в сети интернет 1125=1 обязательны место расчета 1187 с адресом сайта и контакт покупателя 1008. Если при регистрации отсутствовало только место расчета и оно передано в чеке, выполнять 0x1F не требуется.",
			RequestLayout: "tlv", Input: receiptPaymentFields(), Validate: validateReceiptInternet,
			Preconditions: []string{"Normal receipt is open; selected taxation system was enabled during registration."},
		}),
		makeCommand(commandSpec{
			Code: 0x2E, Name: "setCorrectionData", TitleRU: "Передать данные чека коррекции", Category: "correction", Page: 20,
			Description:   "Supply correction data, exact payment amounts and VAT calculation bases, including appendix-10 tags 40007–40012. Payment and VAT-base totals must agree. The nested basis document number uses tag 1023 as printed in this manual.",
			DescriptionRU: "Для передачи данных коррекции необходимо предварительное успешное открытие чека коррекции. Применяемая система налогообложения должна быть одним из значений, указанных при регистрации. Сумма по всем типам оплаты должна равняться сумме всех баз расчета НДС. Основание коррекции — STLV 1174 с наименованием, датой и номером документа; таблица указывает тег 1023 для номера. Дата основания передается UNIXTIME с указанием даты. При 1125=1 обязательны теги 1187 и 1008. Приложение 10 добавляет необязательные VLN-теги 40007–40012 для ставок 5%, 7%, 22% и расчетных ставок. Если применены только новые ставки, ККТ автоматически включает тег 1104 со значением 0.",
			RequestLayout: "tlv", Input: receiptCorrectionFields(), Validate: validateReceiptCorrection,
			Preconditions: []string{"Correction receipt is open; selected taxation system was enabled during registration."},
			Notes:         []string{"Tag 1023 for the correction basis document number follows the supplied protocol literally. Confirm firmware-specific differences with the manufacturer.", "Original VAT-base tags 1102 and 1106 retain their documented historic meanings; the API does not reinterpret fiscal legislation."},
		}),
		makeCommand(commandSpec{
			Code: 0x3F, Name: "setCorrectionDeviceDetails", TitleRU: "Передать данные автоматического устройства расчетов для чека коррекции", Category: "correction", Page: 21,
			Description:   "Supply settlement address/place and optional automatic device number for a correction receipt when the device was registered without those details.",
			DescriptionRU: "Данные автоматического устройства для чека коррекции передаются командой 0x3F, если они не были указаны при регистрации ККТ. Если ККТ зарегистрирована с этими параметрами, команда возвращает ошибку 0x5F. Номер автомата необязателен, но рекомендован ФНС к указанию.",
			RequestLayout: "tlv", Input: receiptDeviceFields(false),
			Preconditions: []string{"Correction receipt is open; KKT was registered without the corresponding automatic-device details."},
		}),
	}
	return commands
}

func receiptDeviceFields(allRequired bool) []ParameterDefinition {
	return []ParameterDefinition{
		receiptParam("settlementAddress", "Адрес расчетов", "ASCII", 1009, true, 0),
		receiptParam("settlementPlace", "Место расчетов", "ASCII", 1187, true, 0),
		receiptParam("automaticDeviceNumber", "Номер автомата", "ASCII", 1036, allRequired, 0),
	}
}

func receiptCorrectionFinalizeFields() []ParameterDefinition {
	sign := receiptParam("calculationSign", "Признак расчета", "BYTE", 0, true, 0)
	sign.Enum = receiptCalculationSigns
	vat := receiptParam("legacyVatRate", "Ставка НДС для конфигураций до 1.0.18", "BYTE", 0, false, 0)
	vat.Min, vat.Max = limit(1), limit(6)
	vat.Notes = "Only supply for firmware older than 1.0.18. Omit for current firmware."
	return []ParameterDefinition{sign, receiptParam("totalKopecks", "Сумма коррекции в копейках", "VLN", 0, true, 0), vat}
}

func receiptItemFields() []ParameterDefinition {
	vat := receiptParam("vatRate", "Ставка НДС", "BYTE", 1199, true, 0)
	vat.Enum = receiptVATRates
	method := receiptParam("paymentMethod", "Признак способа расчета", "BYTE", 1214, true, 0)
	method.Min, method.Max = limit(1), limit(7)
	object := receiptParam("paymentObject", "Признак предмета расчета", "BYTE", 1212, false, 0)
	object.Min, object.Max = limit(1), limit(19)
	agent := receiptParam("agentSign", "Признак агента по предмету расчета", "BYTE", 1222, false, 0)
	agent.Enum = receiptAgentSigns
	quantity := receiptParam("quantity", "Количество предмета расчета", "FVLN", 1023, true, 0)
	quantity.Notes = "Exact non-negative decimal string; never a floating-point JSON number."
	fields := []ParameterDefinition{
		receiptParam("name", "Наименование предмета расчета", "ASCII", 1030, true, 128),
		receiptParam("priceKopecks", "Цена за единицу с учетом скидок и наценок", "VLN", 1079, true, 0), quantity, vat, method, object, agent,
	}
	for _, p := range []ParameterDefinition{
		receiptParam("transferOperatorPhones", "Телефон оператора перевода", "ASCII", 1075, false, 20),
		receiptParam("transferOperation", "Операция платежного агента", "ASCII", 1044, false, 63),
		receiptParam("paymentAgentPhones", "Телефон платежного агента", "ASCII", 1073, false, 20),
		receiptParam("paymentAcceptancePhones", "Телефон оператора по приему платежей", "ASCII", 1074, false, 20),
		receiptParam("transferOperatorName", "Наименование оператора перевода", "ASCII", 1026, false, 127),
		receiptParam("transferOperatorAddress", "Адрес оператора перевода", "ASCII", 1005, false, 164),
		receiptParam("transferOperatorINNs", "ИНН оператора перевода", "ASCII", 1016, false, 12),
		receiptParam("supplierPhones", "Телефон поставщика", "ASCII", 1171, false, 20),
		receiptParam("supplierName", "Наименование поставщика", "ASCII", 1225, false, 64),
	} {
		switch p.Name {
		case "transferOperatorPhones", "paymentAgentPhones", "paymentAcceptancePhones", "transferOperatorINNs", "supplierPhones":
			p.Repeated, p.MinItems, p.JSONType = true, 1, "array"
		}
		p.RequiredIf = "agentSign selects a role requiring this field, unless identical agent data will be supplied by command 0x2C"
		fields = append(fields, p)
	}
	inn := receiptParam("supplierINN", "ИНН поставщика", "ASCII", 1226, false, 12)
	inn.FixedLength, inn.Padding = 12, "space"
	nomenclature := receiptParam("nomenclatureCode", "Код товарной номенклатуры", "ASCII", 1162, false, 0)
	nomenclature.Notes = "ASCII HEX representation, for example 05AB1208; this is not a raw-byte parameter."
	fields = append(fields, inn, nomenclature,
		receiptParam("unit", "Единица измерения предмета расчета", "ASCII", 1197, false, 16),
		receiptParam("exciseKopecks", "Акциз", "VLN", 1229, false, 0),
		receiptParam("customsDeclaration", "Номер таможенной декларации", "ASCII", 1231, false, 32),
		receiptParam("countryCode", "Код страны", "ASCII", 1230, false, 3),
		receiptParam("additionalItemProperty", "Дополнительный реквизит предмета расчета", "ASCII", 1191, false, 64))
	return fields
}

func receiptSharedAgentFields() []ParameterDefinition {
	agent := receiptParam("agentSign", "Признак платежного агента", "BYTE", 1057, true, 0)
	agent.Enum = receiptAgentSigns
	fields := []ParameterDefinition{agent,
		receiptParam("paymentAgentPhone", "Телефон платежного агента", "ASCII", 1073, false, 20),
		receiptParam("transferOperation", "Операция платежного агента", "ASCII", 1044, false, 32),
		receiptParam("paymentAcceptancePhone", "Телефон оператора приема платежей", "ASCII", 1074, false, 20),
		receiptParam("transferOperatorName", "Наименование оператора перевода", "ASCII", 1026, false, 47),
		receiptParam("transferOperatorINN", "ИНН оператора перевода", "ASCII", 1016, false, 12),
		receiptParam("transferOperatorAddress", "Адрес оператора перевода", "ASCII", 1005, false, 95),
		receiptParam("transferOperatorPhone", "Телефон оператора перевода", "ASCII", 1075, false, 20),
		receiptParam("supplierPhone", "Телефон поставщика", "ASCII", 1171, false, 20),
	}
	for i := 1; i < len(fields); i++ {
		fields[i].RequiredIf = "agentSign selects one of the roles specified for this field in the manual"
	}
	return fields
}

func receiptPaymentAmounts() []ParameterDefinition {
	return []ParameterDefinition{
		receiptParam("cashKopecks", "Наличными", "VLN", 1031, true, 0),
		receiptParam("cashlessKopecks", "Безналичными", "VLN", 1081, true, 0),
		receiptParam("prepaymentKopecks", "Предоплатой (зачетом аванса)", "VLN", 1215, true, 0),
		receiptParam("creditKopecks", "Постоплатой (в кредит)", "VLN", 1216, true, 0),
		receiptParam("counterProvisionKopecks", "Встречным предоставлением", "VLN", 1217, true, 0),
	}
}

func receiptPaymentFields() []ParameterDefinition {
	tax := receiptParam("taxSystem", "Применяемая система налогообложения", "BYTE", 1055, true, 0)
	tax.Enum = receiptTaxSystems
	fields := append([]ParameterDefinition{tax}, receiptPaymentAmounts()...)
	cashierINN := receiptParam("cashierINN", "ИНН кассира", "ASCII", 1203, false, 12)
	cashierINN.FixedLength, cashierINN.RequiredIf = 12, "cashier has an INN; cashier data may be omitted in automatic mode"
	fields = append(fields,
		receiptParam("cashierName", "ФИО кассира", "ASCII", 1021, false, 64), cashierINN,
		receiptParam("customerContact", "Адрес электронной почты клиента", "ASCII", 1008, false, 64),
		receiptParam("customerINN", "ИНН покупателя", "ASCII", 1228, false, 12),
		receiptParam("customerName", "Покупатель", "ASCII", 1227, false, 256),
		receiptParam("additionalReceiptProperty", "Дополнительный реквизит кассового чека", "ASCII", 1192, false, 16),
		receiptParam("userPropertyName", "Наименование дополнительного реквизита пользователя", "ASCII", 1085, false, 64),
		receiptParam("userPropertyValue", "Значение дополнительного реквизита пользователя", "ASCII", 1086, false, 256))
	return append(fields, receiptInternetFields()...)
}

func receiptInternetFields() []ParameterDefinition {
	flag := receiptParam("internetPayment", "Признак расчета в сети интернет", "BYTE", 1125, false, 0)
	flag.Enum = map[string]string{"0": "Признак расчета в сети интернет не установлен", "1": "Расчет в сети интернет"}
	place := receiptParam("settlementPlace", "Место расчета", "ASCII", 1187, false, 256)
	place.RequiredIf = "internetPayment equals 1"
	return []ParameterDefinition{flag, place}
}

func receiptCorrectionFields() []ParameterDefinition {
	inn := receiptParam("authorizedPersonINN", "ИНН уполномоченного лица", "ASCII", 1203, true, 12)
	inn.FixedLength = 12
	correctionType := receiptParam("correctionType", "Тип коррекции", "BYTE", 1173, true, 0)
	correctionType.Enum = map[string]string{"0": "Самостоятельно", "1": "По предписанию"}
	tax := receiptParam("taxSystem", "Применяемая система налогообложения", "BYTE", 1055, true, 0)
	tax.Enum = receiptTaxSystems
	fields := []ParameterDefinition{receiptParam("authorizedPersonName", "ФИО уполномоченного лица", "ASCII", 1021, true, 63), inn, correctionType, tax}
	fields = append(fields, receiptPaymentAmounts()...)
	for _, value := range []struct {
		name, title string
		tag         uint16
	}{
		{"vat18BaseKopecks", "Сумма, от которой считается НДС по ставке 18%", 1102},
		{"vat10BaseKopecks", "Сумма, от которой считается НДС по ставке 10%", 1103},
		{"vat0BaseKopecks", "Сумма расчета с НДС 0%", 1104},
		{"noVatBaseKopecks", "Сумма расчета без НДС", 1105},
		{"vat18CalculatedBaseKopecks", "Сумма, от которой считается НДС по расчетной ставке 18/118", 1106},
		{"vat10CalculatedBaseKopecks", "Сумма, от которой считается НДС по расчетной ставке 10/110", 1107},
		{"vat5BaseKopecks", "Сумма, от которой считается НДС по ставке 5%", 40007},
		{"vat7BaseKopecks", "Сумма, от которой считается НДС по ставке 7%", 40008},
		{"vat5CalculatedBaseKopecks", "Сумма, от которой считается НДС по расчетной ставке 5/105", 40009},
		{"vat7CalculatedBaseKopecks", "Сумма, от которой считается НДС по расчетной ставке 7/107", 40010},
		{"vat22BaseKopecks", "Сумма, от которой считается НДС по ставке 22%", 40011},
		{"vat22CalculatedBaseKopecks", "Сумма, от которой считается НДС по расчетной ставке 22/122", 40012},
	} {
		field := receiptParam(value.name, value.title, "VLN", value.tag, false, 0)
		field.RequiredIf = "at least one VAT-base field must be supplied; the sum of supplied bases must equal total payments"
		fields = append(fields, field)
	}
	fields = append(fields, ParameterDefinition{
		Name: "basis", TitleRU: "Основание для коррекции", Description: "Correction basis document, encoded as STLV 1174.", JSONType: "object", WireType: "STLV", Tag: 1174, Required: true,
		Children: []ParameterDefinition{
			receiptParam("name", "Наименование документа основания", "ASCII", 1177, true, 255),
			{Name: "date", TitleRU: "Дата документа основания", Description: "Date-only Unix timestamp, supplied by the caller without an implicit timezone conversion.", JSONType: "integer", WireType: "UNIXTIME", Tag: 1178, Required: true, Notes: "The manual requires a date without minutes and seconds; the caller supplies its intended date in UnixTime form."},
			receiptParam("number", "Номер документа основания", "ASCII", 1023, true, 32),
		},
	})
	fields = append(fields, receiptInternetFields()...)
	return append(fields, receiptParam("customerContact", "Адрес электронной почты или телефон покупателя", "ASCII", 1008, false, 64))
}

func validateReceiptInternet(params map[string]json.RawMessage) error {
	if raw, present := params["internetPayment"]; present {
		value, err := receiptUnsigned(raw)
		if err != nil {
			return err
		}
		if value == 1 {
			for _, name := range []string{"settlementPlace", "customerContact"} {
				var value string
				if json.Unmarshal(params[name], &value) != nil || value == "" {
					return fmt.Errorf("%s is required when internetPayment is 1", name)
				}
			}
		}
	}
	return nil
}

func validateReceiptItem(params map[string]json.RawMessage) error {
	item, err := decodeParams(params["item"])
	if err != nil {
		return fmt.Errorf("item: %w", err)
	}
	if raw, present := item["nomenclatureCode"]; present {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		if _, err := hex.DecodeString(value); err != nil {
			return fmt.Errorf("nomenclatureCode must be a hexadecimal string with an even number of digits")
		}
	}
	if raw, present := item["paymentObject"]; present {
		object, err := receiptUnsigned(raw)
		if err != nil {
			return err
		}
		if object == 15 || object == 16 {
			var name string
			if err := json.Unmarshal(item["name"], &name); err != nil {
				return err
			}
			n, err := strconv.Atoi(name)
			if err != nil || n < 1 || n > 31 || strconv.Itoa(n) != name {
				return fmt.Errorf("item.name must be a code 1–31 from appendix 7 for paymentObject 15 or 16")
			}
		}
	}
	agent := uint64(0)
	if raw, present := item["agentSign"]; present {
		agent, err = receiptUnsigned(raw)
		if err != nil {
			return err
		}
	}
	agentFields := []string{"transferOperatorPhones", "transferOperation", "paymentAgentPhones", "paymentAcceptancePhones", "transferOperatorName", "transferOperatorAddress", "transferOperatorINNs", "supplierPhones", "supplierName", "supplierINN"}
	if agent == 0 {
		for _, name := range agentFields {
			if _, present := item[name]; present {
				return fmt.Errorf("item.%s requires a nonzero agentSign", name)
			}
		}
	} else {
		hasAgentData := false
		for _, name := range agentFields {
			if _, present := item[name]; present {
				hasAgentData = true
			}
		}
		// A role with no per-item DAP is permitted because the manual allows
		// shared data to follow through 0x2C. Once per-item DAP is supplied,
		// enforce the documented role-specific required fields.
		if hasAgentData {
			required := []string{"supplierPhones"}
			if agent == 1 || agent == 2 {
				required = append(required, "transferOperation", "transferOperatorPhones", "transferOperatorName", "transferOperatorAddress", "transferOperatorINNs")
			}
			if agent == 4 || agent == 8 {
				required = append(required, "paymentAgentPhones", "paymentAcceptancePhones")
			}
			for _, name := range required {
				raw, present := item[name]
				if !present {
					return fmt.Errorf("item.%s is required when per-item agent data is supplied for the selected agentSign", name)
				}
				var text string
				if json.Unmarshal(raw, &text) == nil && text == "" {
					return fmt.Errorf("item.%s cannot be empty", name)
				}
			}
		}
	}
	return nil
}

func validateReceiptSharedAgent(params map[string]json.RawMessage) error {
	agent, err := receiptUnsigned(params["agentSign"])
	if err != nil {
		return err
	}
	if agent == 0 {
		if len(params) != 1 {
			return fmt.Errorf("agentSign 0 must be supplied without other agent fields")
		}
		return nil
	}
	required := []string{"supplierPhone"}
	if agent == 1 || agent == 2 {
		required = append(required, "transferOperation", "transferOperatorName", "transferOperatorINN", "transferOperatorAddress", "transferOperatorPhone")
	}
	if agent == 4 || agent == 8 {
		required = append(required, "paymentAgentPhone", "paymentAcceptancePhone")
	}
	for _, name := range required {
		var value string
		if err := json.Unmarshal(params[name], &value); err != nil || value == "" {
			return fmt.Errorf("%s is required for the selected agentSign", name)
		}
	}
	return nil
}

func validateReceiptCorrection(params map[string]json.RawMessage) error {
	if err := validateReceiptInternet(params); err != nil {
		return err
	}
	payments := new(big.Int)
	for _, name := range []string{"cashKopecks", "cashlessKopecks", "prepaymentKopecks", "creditKopecks", "counterProvisionKopecks"} {
		value, err := receiptUnsigned(params[name])
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		payments.Add(payments, new(big.Int).SetUint64(value))
	}
	bases := new(big.Int)
	count := 0
	for _, name := range []string{"vat18BaseKopecks", "vat10BaseKopecks", "vat0BaseKopecks", "noVatBaseKopecks", "vat18CalculatedBaseKopecks", "vat10CalculatedBaseKopecks", "vat5BaseKopecks", "vat7BaseKopecks", "vat5CalculatedBaseKopecks", "vat7CalculatedBaseKopecks", "vat22BaseKopecks", "vat22CalculatedBaseKopecks"} {
		if raw, present := params[name]; present {
			value, err := receiptUnsigned(raw)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			bases.Add(bases, new(big.Int).SetUint64(value))
			count++
		}
	}
	if count == 0 {
		return fmt.Errorf("at least one VAT-base field must be supplied")
	}
	if payments.Cmp(bases) != 0 {
		return fmt.Errorf("correction payment total %s does not equal VAT-base total %s", payments, bases)
	}
	return nil
}

func receiptUnsigned(raw json.RawMessage) (uint64, error) {
	var value string
	if len(raw) != 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, err
		}
	} else {
		value = string(raw)
	}
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("value must be an unsigned integer")
	}
	return n, nil
}

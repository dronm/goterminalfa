package service

import (
	"encoding/json"
	"fmt"
	"time"
)

const ffd12ReceiptDocument = "Протокол информационного обмена ККТ Терминал-ФА — дополнение для работы с ФФД версии 1.2"

// ffd12ReceiptCommands returns only the receipt definitions replaced or added by
// the FFD 1.2 profile. The profile catalogue merges these into a separate copy of
// the base catalogue, preserving the base API and wire layouts.
func ffd12ReceiptCommands() []Command {
	result := make([]Command, 0, len(ffd12ReceiptSpecs()))
	for _, spec := range ffd12ReceiptSpecs() {
		command := makeCommand(spec)
		command.Definition.Source = Source{Document: ffd12ReceiptDocument, Version: "FFD 1.2", Page: spec.Page}
		command.Definition.OperationRequired = true
		result = append(result, command)
	}
	return result
}

func ffd12ReceiptSpecs() []commandSpec {
	resultFields := append(receiptResultFields(),
		receiptParam("dateTime", "Дата-время формирования чека", "DATETIME5", 0, true, 0),
		receiptParam("shiftNumber", "Номер смены", "UINT16", 0, true, 0))
	sign := receiptParam("calculationSign", "Признак расчета", "BYTE", 0, true, 0)
	sign.Enum = receiptCalculationSigns
	total := receiptParam("totalKopecks", "Итог чека в копейках", "UINT40", 0, true, 0)
	printing := receiptParam("printSuppressionCode", "Необязательный параметр управления печатной формой", "UINT16", 0, false, 0)
	printing.Enum = map[string]string{"41029": "Не формировать печатную форму документа"}
	printing.Notes = "Optional UINT16 LE. The documented value 41029 suppresses the printed form; omit for normal printing. Other numeric values are not assigned a documented meaning."
	tax := receiptParam("taxSystem", "Режим налогообложения", "BYTE", 1055, true, 0)
	tax.Enum = map[string]string{
		"1": "Общая", "2": "Упрощенная доход", "4": "Упрощенная доход минус расход",
		"16": "Единый сельскохозяйственный налог", "32": "Патентная система налогообложения",
	}
	paymentFields := append([]ParameterDefinition{tax}, receiptPaymentAmounts()...)
	return []commandSpec{
		{
			Code: 0x19, Name: "addCashlessPaymentDetails", TitleRU: "Передать сведения об оплате безналичными", Category: "receipts", Page: 27,
			Description:   "Supply the input TLVs for details of one cashless payment after payment data. The KKT forms STLV 1234–1235; this command is prohibited for correction receipts.",
			DescriptionRU: "Данная команда формирует STLV 1234–1235 (сведения обо всех оплатах по чеку безналичными). Может быть выполнена после передачи данных оплаты (0x2D). Для кассовых чеков коррекции команда недопустима и вернет ошибку 0x28. Дополнительные сведения о безналичной оплате необязательны. Справочник признака способа оплаты безналичными в документе не актуализирован ФНС.",
			RequestLayout: "tlv", Input: []ParameterDefinition{
				receiptParam("amountKopecks", "Сумма оплаты безналичными", "VLN", 1082, true, 0),
				receiptParam("paymentMethod", "Признак способа оплаты безналичными", "BYTE", 1236, true, 0),
				receiptParam("identifiers", "Идентификаторы безналичной оплаты", "ASCII", 1237, true, 256),
				receiptParam("additionalDetails", "Дополнительные сведения о безналичной оплате", "ASCII", 1238, false, 256),
			},
			Preconditions: []string{"A normal receipt is open and command 0x2D has succeeded; this command is not valid for correction receipts."},
		},
		{
			Code: 0x23, Name: "beginReceipt", TitleRU: "Открыть кассовый чек", Category: "receipts", Page: 22,
			Description:   "Begin an FFD 1.2 receipt. An optional UINT16 printing parameter of 41029 suppresses its printed form. The shift must be open and no receipt may already be open.",
			DescriptionRU: "Входной параметр UINT16 LE необязателен. При значении 41029 печатная форма документа формироваться не будет. Смена должна быть открыта, другой чек не должен быть начат, а дата и время не должны превосходить более чем на 24 часа дату и время открытия смены. Выходные данные отсутствуют.",
			Input:         []ParameterDefinition{printing},
			Preconditions: []string{"Shift is open; no other receipt is open; shift age is at most 24 hours."},
		},
		{
			Code: 0x24, Name: "finalizeReceipt", TitleRU: "Сформировать чек", Category: "receipts", Page: 27,
			Description:   "Finalize an FFD 1.2 receipt with a calculation sign and a five-byte UINT40 total in kopecks. The response is exactly 17 bytes: fiscal identifiers, local date/time and shift number.",
			DescriptionRU: "Команда завершает процедуру формирования чека. Предварительно должен быть открыт чек и переданы все необходимые данные. Вход: признак расчета BYTE (1 — приход, 2 — возврат прихода, 3 — расход, 4 — возврат расхода), итог чека UINT40 LE в копейках. Ответ: номер чека за смену UINT16 LE, номер ФД UINT32 LE, фискальный признак UINT32 LE, дата-время YMDHm (5 байт), номер смены UINT16 LE. Дополнительный текст в формате входа данной команды не предусмотрен.",
			Input:         []ParameterDefinition{sign, total}, Output: resultFields,
			Preconditions: []string{"Normal receipt is open and all required receipt data has been supplied."},
		},
		{
			Code: 0x25, Name: "beginCorrectionReceipt", TitleRU: "Открыть кассовый чек коррекции", Category: "correction", Page: 22,
			Description:   "Begin an FFD 1.2 correction receipt. The optional UINT16 printing parameter has the same layout as beginReceipt; 41029 suppresses the printed form.",
			DescriptionRU: "Входной параметр UINT16 LE необязателен. При значении 41029 печатная форма документа формироваться не будет. Смена должна быть открыта, другой чек не должен быть начат, а дата и время не должны превосходить более чем на 24 часа дату и время открытия смены. Выходные данные отсутствуют. Отдельный параметр типа коррекции в этой таблице не указан.",
			Input:         []ParameterDefinition{printing},
			Preconditions: []string{"Shift is open; no other receipt is open; shift age is at most 24 hours."},
		},
		{
			Code: 0x26, Name: "finalizeCorrectionReceipt", TitleRU: "Сформировать чек коррекции", Category: "correction", Page: 28,
			Description:   "Finalize an FFD 1.2 correction receipt with the calculation sign and UINT40 total. This profile does not use the base protocol's VLN total or legacy VAT parameter.",
			DescriptionRU: "Команда завершает процедуру формирования чека коррекции. Предварительно должен быть открыт чек и переданы все необходимые данные. Вход: признак расчета BYTE (1–4), итог чека UINT40 LE в копейках (5 байт). Ответ: номер чека UINT16 LE, номер ФД UINT32 LE, фискальный признак UINT32 LE, дата-время YMDHm (5 байт), номер смены UINT16 LE.",
			Input:         []ParameterDefinition{sign, total}, Output: resultFields,
			Preconditions: []string{"Correction receipt is open and all FFD 1.2 correction receipt data has been supplied."},
		},
		{
			Code: 0x2D, Name: "setReceiptPayment", TitleRU: "Передать данные оплаты для кассового чека или чека коррекции", Category: "receipts", Page: 27,
			Description:   "Supply FFD 1.2 payment TLVs for a normal or correction receipt. Only the taxation system and five explicit payment amounts belong to this command; extra customer/cashier attributes use command 0x98 in the supplement.",
			DescriptionRU: "Каждый параметр представляет TLV. Режим налогообложения: 1 — общая, 2 — УСН доход, 4 — УСН доход минус расход, 16 — ЕСХН, 32 — патент. Суммы наличными (1031), безналичными (1081), предоплатой (1215), постоплатой (1216), встречным предоставлением (1217) передаются VLN в копейках. Выходные данные отсутствуют. Команда применяется как для обычного чека, так и для чека коррекции.",
			RequestLayout: "tlv", Input: paymentFields,
			Preconditions: []string{"Normal or correction receipt is open; the chosen taxation system is enabled in the KKT registration."},
		},
		{
			Code: 0x96, Name: "addReceiptItemUnmarked", TitleRU: "Передать данные предмета расчета за обычный (немаркированный) товар", Category: "receipts", Page: 22,
			Description:   "Supply one ordinary unmarked item as the flat input TLVs documented for FFD 1.2. Quantity is an exact decimal string; the omitted measure defaults to pieces in the KKT. Agent details and repeated industry STLVs are validated when supplied.",
			DescriptionRU: "Для обычного (немаркированного) товара передаются перечисленные TLV без дополнительной оболочки STLV 1059. Необязательные теги помечены в документе символом *. Если мера количества 2108 отсутствует, ККТ использует 0 — штуки, единицы. Код страны дополняется справа пробелами до 3 символов. ИНН поставщика дополняется до 12 символов и обязателен для всех типов агента. Каждый повторяемый отраслевой реквизит STLV 1260 обязательно содержит теги 1262, 1263, 1264 и 1265. Ставки НДС дополнены приложением 5: 1–12, включая 22% и 22/122; применимость ставки к дате операции проверяет ККТ.",
			RequestLayout: "tlv", Input: ffd12UnmarkedItemFields(), Validate: validateFFD12UnmarkedItem,
			Preconditions: []string{"A normal or correction receipt is open; this command is for ordinary unmarked goods; any required FFD 1.2 activation is already configured on the KKT."},
			Notes:         []string{"Appendix 5 provides VAT values and dated transition notes; the driver encodes the selected value and does not infer tax applicability from the host clock.", "Country codes and supplier INNs use the right-space padding explicitly documented by the supplement.", "Customer/cashier and correction-specific additional receipt attributes are described separately by command 0x98."},
		},
	}
}

func ffd12UnmarkedItemFields() []ParameterDefinition {
	quantity := receiptParam("quantity", "Количество предмета расчета", "FVLN", 1023, true, 0)
	quantity.Notes = "Exact non-negative decimal string, not a floating-point JSON number."
	measure := receiptParam("measure", "Мера количества предмета расчета", "BYTE", 2108, false, 0)
	measure.Enum = map[string]string{
		"0": "Штуки, единицы", "10": "Г", "11": "Кг", "12": "Т", "20": "См", "21": "Дм", "22": "М",
		"30": "Кв. см", "31": "Кв. дм", "32": "Кв. м", "40": "Мл", "41": "Л", "42": "Куб. м",
		"50": "КВт ч", "51": "Гкал", "70": "Сутки", "71": "Час", "72": "Минута", "73": "Секунда",
		"80": "Кбайт", "81": "Мбайт", "82": "Гбайт", "83": "Тбайт", "255": "Иные случаи",
	}
	measure.Notes = "Omission is preserved on the wire; the KKT assigns documented default 0 (pieces/units)."
	vat := receiptParam("vatRate", "Ставка НДС", "BYTE", 1199, true, 0)
	vat.Enum = receiptVATRates
	method := receiptParam("paymentMethod", "Признак способа расчета", "BYTE", 1214, true, 0)
	method.Min, method.Max = limit(1), limit(7)
	object := receiptParam("paymentObject", "Признак предмета расчета", "BYTE", 1212, true, 0)
	object.Enum = map[string]string{
		"1": "Товар", "2": "Подакцизный товар", "3": "Работа", "4": "Услуга", "5": "Ставка в азартной игре",
		"6": "Выигрыш в азартной игре", "7": "Лотерея", "8": "Выигрыш в лотерее", "9": "Права на результаты интеллектуальной деятельности",
		"10": "Аванс, задаток, предоплата, кредит", "11": "Вознаграждение агента", "12": "Взнос, пени, штраф, вознаграждение, бонус",
		"13": "Иной предмет расчета", "14": "Имущественные права", "15": "Внереализационный доход", "16": "Расходы, уменьшающие налог",
		"17": "Торговый сбор", "18": "Курортный сбор", "19": "Залог", "20": "Расходы, уменьшающие доход", "21": "Взносы ОПС ИП",
		"22": "Взносы ОПС организаций и ИП", "23": "Взносы ОМС ИП", "24": "Взносы ОМС организаций и ИП", "25": "Взносы социального страхования",
		"26": "Обменные знаки игорного заведения", "27": "Выдача денег банковским платежным агентом",
		"30": "Маркируемый подакцизный товар без кода маркировки", "31": "Маркируемый подакцизный товар с кодом маркировки",
		"32": "Маркируемый товар без кода маркировки", "33": "Маркируемый товар с кодом маркировки",
	}
	country := receiptParam("countryCode", "Код страны", "ASCII", 1230, false, 3)
	country.FixedLength, country.Padding = 3, "space"
	agent := receiptParam("agentSign", "Признак агента по предмету расчета", "BYTE", 1222, false, 0)
	agent.Enum = receiptAgentSigns
	fields := []ParameterDefinition{
		receiptParam("name", "Наименование предмета расчета", "ASCII", 1030, true, 128),
		receiptParam("priceKopecks", "Цена за единицу с учетом скидок и наценок", "VLN", 1079, true, 0),
		quantity, measure, vat, method, object,
		receiptParam("barcode", "Штрих-код товара", "ASCII", 2000, false, 256),
		receiptParam("exciseKopecks", "Акциз", "VLN", 1229, false, 0),
		receiptParam("customsDeclaration", "Номер таможенной декларации", "ASCII", 1231, false, 32),
		country, receiptParam("additionalItemProperty", "Дополнительный реквизит предмета расчета", "ASCII", 1191, false, 64), agent,
	}
	for _, field := range []ParameterDefinition{
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
		switch field.Name {
		case "transferOperatorPhones", "paymentAgentPhones", "paymentAcceptancePhones", "transferOperatorINNs", "supplierPhones":
			field.Repeated, field.MinItems, field.JSONType = true, 1, "array"
		}
		switch field.Name {
		case "transferOperatorPhones", "transferOperation", "transferOperatorName", "transferOperatorAddress", "transferOperatorINNs":
			field.Notes = "Optional tag (* in the supplement); applicable to banking payment agents and banking payment subagents."
		case "paymentAgentPhones", "paymentAcceptancePhones":
			field.Notes = "Optional tag (* in the supplement); applicable to payment agents and payment subagents."
		case "supplierPhones", "supplierName":
			field.Notes = "Optional tag (* in the supplement); applicable to all agent roles."
		}
		fields = append(fields, field)
	}
	inn := receiptParam("supplierINN", "ИНН поставщика", "ASCII", 1226, false, 12)
	inn.FixedLength, inn.Padding = 12, "space"
	inn.RequiredIf = "agentSign is nonzero; required for all agent roles"
	industry := ParameterDefinition{
		Name: "industryFields", TitleRU: "Отраслевой реквизит предмета расчета", Description: "Repeated industry STLV 1260; every entry contains all four child tags.",
		JSONType: "array", WireType: "STLV", Tag: 1260, Repeated: true, MinItems: 1,
		Children: []ParameterDefinition{
			{Name: "authority", TitleRU: "Идентификатор органа власти", Description: "Authority identifier from appendix 1; integer 1–72.", JSONType: "integer", WireType: "BYTE", Tag: 1262, Required: true, Min: limit(1), Max: limit(72)},
			{Name: "documentDate", TitleRU: "Дата документа основания", Description: "Calendar date in DD.MM.YYYY format.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 1263, FixedLength: 10, Required: true},
			receiptParam("documentNumber", "Номер документа основания", "ASCII", 1264, true, 32),
			receiptParam("value", "Значение отраслевого реквизита", "ASCII", 1265, true, 256),
		},
	}
	return append(fields, inn, industry)
}

func validateFFD12UnmarkedItem(params map[string]json.RawMessage) error {
	if raw, present := params["industryFields"]; present {
		var entries []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil {
			return fmt.Errorf("industryFields must be an array of objects")
		}
		for i, entry := range entries {
			var text string
			if err := json.Unmarshal(entry["documentDate"], &text); err != nil {
				return fmt.Errorf("industryFields[%d].documentDate must be a string", i)
			}
			date, err := time.Parse("02.01.2006", text)
			if err != nil || date.Format("02.01.2006") != text {
				return fmt.Errorf("industryFields[%d].documentDate must be a valid DD.MM.YYYY date", i)
			}
		}
	}
	agent := uint64(0)
	if raw, present := params["agentSign"]; present {
		value, err := receiptUnsigned(raw)
		if err != nil {
			return err
		}
		agent = value
	}
	// All starred agent details remain optional. Only the supplier INN row has
	// an explicit exception: it is mandatory for every nonzero agent role.
	// Unlike the base protocol, this supplement does not prohibit optional
	// agent-detail tags when agentSign is zero; do not invent that restriction.
	if agent != 0 {
		raw, present := params["supplierINN"]
		if !present {
			return fmt.Errorf("supplierINN is required for the selected agentSign")
		}
		var text string
		if json.Unmarshal(raw, &text) != nil || text == "" {
			return fmt.Errorf("supplierINN must be a non-empty string for the selected agentSign")
		}
	}
	return nil
}

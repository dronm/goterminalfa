package service

import (
	"encoding/json"
	"fmt"
	"time"
)

// ffd12ExtraCommands provides the unchanged report-wire definitions with their
// supplement provenance, optional report data, and the FFD 1.2 receipt extras.
// It returns replacements/additions; the caller owns merging by protocol code.
func ffd12ExtraCommands(base []Command) []Command {
	commands := []Command{}
	for _, code := range []string{"0x27", "0x28"} {
		for _, original := range base {
			if original.Definition.ProtocolCode != code {
				continue
			}
			cmd := original
			cmd.Definition.Source = ffd12ExtraSource(12)
			cmd.Definition.Notes = append(append([]string{}, cmd.Definition.Notes...), "The FFD 1.2 supplement preserves this command's base wire layout. Optional report details may be sent using 0xE6 between report start and completion.")
			cmd.Definition.DescriptionRU += " Дополнение для ФФД 1.2 сохраняет этот формат команды. При необходимости между началом и завершением отчета передаются данные командой 0xE6."
			commands = append(commands, cmd)
			break
		}
	}

	report := makeCommand(commandSpec{
		Code: 0xE6, Name: "sendSettlementReportData", Category: "reports", Page: 12, RequestLayout: "tlv",
		TitleRU:       "Передать данные отчета о состоянии расчетов",
		Description:   "Optionally supply settlement report address/place and a paired additional property and raw data. Execute only between beginning and finalizing a settlement report. When this command is used its address and place fields are required.",
		DescriptionRU: "Выполнение команды не является обязательным в процессе формирования отчета о текущем состоянии расчетов. Выполняется при необходимости передать соответствующие данные. Адрес формирования ФД: тег 1009, ASCII, не более 256 символов. Место формирования ФД: тег 1187, ASCII, не более 256 символов. Дополнительный реквизит отчета: тег 1280, ASCII, не более 32 символов; дополнительные данные отчета: тег 1281, BYTE(N), не более 32 байт. Теги 1280 и 1281 необязательны, при необходимости включения одновременно указываются оба значения. Выходные данные отсутствуют.",
		Input: []ParameterDefinition{
			ffd12ExtraText("documentAddress", "Адрес формирования ФД", 1009, 256, true),
			ffd12ExtraText("documentPlace", "Место формирования ФД", 1187, 256, true),
			ffd12ExtraText("reportAttribute", "Дополнительный реквизит отчета", 1280, 32, false),
			{Name: "reportDataHex", TitleRU: "Дополнительные данные отчета", Description: "Additional report data represented as hexadecimal, at most 32 decoded bytes. Supply together with reportAttribute.", JSONType: "string", WireType: "RAW", Tag: 1281, MaxLength: 32, RequiredIf: "reportAttribute is present"},
		},
		Preconditions: []string{"A settlement report has been started with 0x27 and has not yet been finalized with 0x28."},
		Validate: func(params map[string]json.RawMessage) error {
			_, property := params["reportAttribute"]
			_, data := params["reportDataHex"]
			if property != data {
				return fmt.Errorf("reportAttribute and reportDataHex must be supplied together")
			}
			return nil
		},
	})
	report.Definition.Input[2].RequiredIf = "reportDataHex is present"
	report.Definition.Source = ffd12ExtraSource(12)
	commands = append(commands, report)

	receipt := makeCommand(commandSpec{
		Code: 0x98, Name: "setReceiptAdditionalData", Category: "receipts", Page: 26, RequestLayout: "tlv",
		TitleRU:       "Передать дополнительные реквизиты кассового чека (чека коррекции)",
		Description:   "Supply optional receipt/customer/cashier details, settlement details and repeated industry attributes for an FFD 1.2 receipt. A correction receipt must include correctedCalculationDate directly as UnixTime tag 1178. Prescription number tag 1179 is included only for an authority-ordered correction. This command has no legacy correction-basis STLV wrapper.",
		DescriptionRU: "Для кассового чека команда обязательна только при необходимости указания требуемых данных: сведения о покупателе, адрес, место и номер автоматического устройства для ККТ, работающих в режиме развозной торговли или один-ко-многим. Для кассового чека коррекции выполнение команды обязательно: передается дата совершения корректируемого расчета, тег 1178, UNIXTIME. Эта дата не может быть позже даты формирования чека коррекции. При отсутствии достоверных сведений указывается дата из расчетного периода корректируемого расчета. Номер предписания налогового органа, тег 1179, ASCII до 32 символов, включается только при коррекции по предписанию. Отраслевой реквизит чека, STLV 1261, повторяемый; каждый объект содержит все теги 1262, 1263, 1264 и 1265. Выходные данные отсутствуют.",
		Input:         ffd12ReceiptAdditionalFields(),
		Notes: []string{
			"Fields are top-level TLVs. The supplement does not specify the legacy 1173/1174/1177 correction-basis fields in command 0x98.",
			"The table visually swaps the final two columns for prescription number. The accompanying page-27 prose identifies it as tag 1179 with a 32-character bound.",
			"For correction receipts the surrounding operation must require correctedCalculationDate. The request alone does not state whether a tax-authority prescription applies; include prescriptionNumber only when it does.",
			"correctedCalculationDate is a caller-supplied Unix timestamp. No implicit host timezone conversion is applied, and the caller must ensure the corrected date does not follow the KKT document-formation date.",
		},
		Preconditions: []string{"A normal or correction receipt is open. Correction receipts require correctedCalculationDate before finalization; the supplement specifies no order relative to payment data 0x2D."},
		Validate:      ffd12ValidateReceiptAdditionalData,
	})
	receipt.Definition.Source = ffd12ExtraSource(26)
	commands = append(commands, receipt)
	return commands
}

func ffd12ExtraSource(page int) Source {
	return Source{Document: "Протокол информационного обмена ККТ Терминал-ФА: дополнение для работы с ФФД версии 1.2", Version: "FFD 1.2", Page: page}
}

func ffd12ExtraText(name, title string, tag uint16, max int, required bool) ParameterDefinition {
	return ParameterDefinition{Name: name, TitleRU: title, Description: title, JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: tag, Required: required, MaxLength: max}
}

func ffd12ReceiptAdditionalFields() []ParameterDefinition {
	fields := []ParameterDefinition{
		{Name: "internetPayment", TitleRU: "Признак расчета в сети интернет", Description: "Included in the fiscal document when equal to 1; 0 remains an explicit supplied byte.", JSONType: "integer", WireType: "BYTE", Tag: 1125, Enum: map[string]string{"0": "Not an internet payment", "1": "Internet payment"}},
		{Name: "timeZone", TitleRU: "Часовая зона места расчета", Description: "Used when marked goods are present; numeric value from the Russian classifier. The supplement does not include that classifier's enumeration.", JSONType: "integer", WireType: "BYTE", Tag: 1011},
		ffd12ExtraText("settlementAddress", "Адрес расчетов", 1009, 256, false),
		ffd12ExtraText("settlementPlace", "Место расчетов", 1187, 256, false),
		ffd12ExtraText("automaticDeviceNumber", "Номер автоматического устройства", 1036, 20, false),
		ffd12ExtraText("cashierName", "Кассир", 1021, 64, false),
		ffd12ExtraText("cashierINN", "ИНН кассира", 1203, 12, false),
		ffd12ExtraText("customerContact", "Адрес электронной почты или номер телефона покупателя", 1008, 64, false),
		ffd12ExtraText("customerINN", "ИНН покупателя", 1228, 12, false),
		ffd12ExtraText("customerName", "Покупатель (клиент)", 1227, 256, false),
		ffd12ExtraText("customerBirthDate", "Дата рождения покупателя", 1243, 10, false),
		ffd12ExtraText("customerCitizenship", "Гражданство покупателя", 1244, 3, false),
		{Name: "customerDocumentType", TitleRU: "Код вида документа, удостоверяющего личность", Description: "Integer document-type code from Appendix 1, page 34.", JSONType: "integer", WireType: "BYTE", Tag: 1245, Enum: ffd12CustomerDocumentTypes()},
		ffd12ExtraText("customerDocumentData", "Данные документа, удостоверяющего личность", 1246, 64, false),
		ffd12ExtraText("customerAddress", "Адрес покупателя", 1254, 256, false),
		ffd12ExtraText("additionalReceiptProperty", "Дополнительный реквизит кассового чека", 1192, 16, false),
		ffd12ExtraText("userPropertyName", "Наименование дополнительного реквизита пользователя", 1085, 64, false),
		ffd12ExtraText("userPropertyValue", "Значение дополнительного реквизита пользователя", 1086, 256, false),
		{Name: "industryAttributes", TitleRU: "Отраслевой реквизит чека", Description: "Repeated industry attributes. Each object must include authorityCode, documentDate, documentNumber, and value.", JSONType: "array", WireType: "STLV", Tag: 1261, Repeated: true, MinItems: 1,
			Children: []ParameterDefinition{
				{Name: "authorityCode", TitleRU: "Идентификатор органа власти", Description: "Integer federal authority code, Appendix 1 pages 32–33 (001–072 are transmitted as BYTE values 1–72).", JSONType: "integer", WireType: "BYTE", Tag: 1262, Required: true, Min: limit(1), Max: limit(72)},
				ffd12ExtraText("documentDate", "Дата документа основания", 1263, 10, true),
				ffd12ExtraText("documentNumber", "Номер документа основания", 1264, 32, true),
				ffd12ExtraText("value", "Значение отраслевого реквизита", 1265, 256, true),
			}},
		{Name: "correctedCalculationDate", TitleRU: "Дата совершения корректируемого расчета", Description: "Direct UnixTime tag 1178 for the calculation being corrected, not a nested legacy correction-basis field.", JSONType: "integer", WireType: "UNIXTIME", Tag: 1178, RequiredIf: "The current receipt is a correction receipt", Notes: "Must not be later than the KKT's correction receipt formation date; the caller supplies its intended Unix timestamp without an implicit timezone conversion."},
		ffd12ExtraText("prescriptionNumber", "Номер предписания налогового органа", 1179, 32, false),
	}
	for i := range fields {
		switch fields[i].Name {
		case "settlementPlace":
			fields[i].RequiredIf = "internetPayment equals 1; then this value contains the settlement website address"
		case "customerINN":
			fields[i].FixedLength, fields[i].Padding = 12, "space"
		case "customerCitizenship":
			fields[i].FixedLength, fields[i].Padding = 3, "space"
		case "customerBirthDate":
			fields[i].FixedLength = 10
		case "prescriptionNumber":
			fields[i].RequiredIf = "Correction is performed under a tax-authority prescription; omit otherwise"
		}
	}
	return fields
}

func ffd12ValidateReceiptAdditionalData(params map[string]json.RawMessage) error {
	if raw, present := params["internetPayment"]; present {
		n, err := unsigned(raw, false)
		if err != nil {
			return err
		}
		if n == 1 {
			var place string
			if err := json.Unmarshal(params["settlementPlace"], &place); err != nil || place == "" {
				return fmt.Errorf("settlementPlace containing the website address is required when internetPayment is 1")
			}
		}
	}
	if raw, present := params["customerBirthDate"]; present {
		if err := ffd12ExtraCalendarDate(raw, "customerBirthDate"); err != nil {
			return err
		}
	}
	if raw, present := params["industryAttributes"]; present {
		var values []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for i, value := range values {
			if err := ffd12ExtraCalendarDate(value["documentDate"], fmt.Sprintf("industryAttributes[%d].documentDate", i)); err != nil {
				return err
			}
		}
	}
	if _, prescription := params["prescriptionNumber"]; prescription {
		if _, date := params["correctedCalculationDate"]; !date {
			return fmt.Errorf("prescriptionNumber requires correctedCalculationDate")
		}
	}
	return nil
}

func ffd12ExtraCalendarDate(raw json.RawMessage, name string) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("%s must be a DD.MM.YYYY string", name)
	}
	if len(value) != 10 {
		return fmt.Errorf("%s must use DD.MM.YYYY", name)
	}
	if _, err := time.Parse("02.01.2006", value); err != nil {
		return fmt.Errorf("%s must be a valid DD.MM.YYYY date", name)
	}
	return nil
}

func ffd12CustomerDocumentTypes() map[string]string {
	return map[string]string{
		"21": "Паспорт гражданина РФ", "22": "Паспорт гражданина РФ для использования за пределами РФ",
		"26": "Временное удостоверение личности гражданина РФ", "27": "Свидетельство о рождении гражданина РФ",
		"28": "Иной документ гражданина РФ", "31": "Паспорт иностранного гражданина", "32": "Иной документ иностранного гражданина",
		"33": "Иностранный документ лица без гражданства", "34": "Вид на жительство лица без гражданства",
		"35": "Разрешение на временное проживание лица без гражданства", "36": "Свидетельство о рассмотрении ходатайства о признании беженцем",
		"37": "Удостоверение беженца", "38": "Иной документ лица без гражданства", "40": "Временный документ на период рассмотрения вопроса о гражданстве",
	}
}

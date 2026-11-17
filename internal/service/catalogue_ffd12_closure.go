package service

import (
	"encoding/json"
	"fmt"
)

// ffd12FiscalStorageClosureCommands defines the closure report selected by 0x14.
// The finalizer shares wire byte 0x28 with the report selected by 0x27; their
// canonical names and response layouts remain distinct.
func ffd12FiscalStorageClosureCommands() []Command {
	specs := []commandSpec{
		{
			Code: 0x14, Name: "beginFiscalStorageClosure", TitleRU: "Начать формирование отчета о закрытии ФН", Category: "registration", Page: 13,
			Description:   "Begin the fiscal-storage closure report. This selects the closure meaning of finalizer 0x28, whose result contains the fiscal document number and fiscal sign only.",
			DescriptionRU: "Начать формирование отчета о закрытии ФН (0x14). Входные параметры и выходные данные отсутствуют. Команда начинает процедуру формирования фискального документа «Отчет о закрытии ФН». ФН должен быть в состоянии «Фискальный режим» или «Постфискальный режим».",
			Preconditions: []string{"The fiscal storage must be in fiscal or post-fiscal mode.", "Finish with finalizeFiscalStorageClosure (0x28) in the same complete operation."},
		},
		{
			Code: 0x17, Name: "sendFiscalStorageClosureData", TitleRU: "Передать данные отчета о закрытии ФН", Category: "registration", Page: 13, RequestLayout: "tlv",
			Description:   "Optionally supply closure report address/place and a paired additional attribute and binary data between 0x14 and closure finalization 0x28. Address and place are required when this optional command is supplied.",
			DescriptionRU: "Передать данные отчета о закрытии ФН (0x17). Входные TLV: адрес формирования ФД, тег 1009, ASCII, не более 256 символов; место формирования ФД, тег 1187, ASCII, не более 256 символов; дополнительный реквизит отчета, тег 1282, ASCII, не более 32 символов; дополнительные данные отчета, тег 1283, BYTE(N), не более 32 байт. Теги 1282 и 1283 необязательны, при включении указываются одновременно. Выходные данные отсутствуют. Выполнение команды в процессе закрытия ФН необязательно.",
			Input: []ParameterDefinition{
				ffd12ExtraText("documentAddress", "Адрес формирования ФД", 1009, 256, true),
				ffd12ExtraText("documentPlace", "Место формирования ФД", 1187, 256, true),
				{Name: "reportAttribute", TitleRU: "Дополнительный реквизит отчета о закрытии ФН", Description: "Optional closure report attribute, at most 32 CP866 bytes. Supply together with reportDataHex.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: 1282, MaxLength: 32, RequiredIf: "reportDataHex is present"},
				{Name: "reportDataHex", TitleRU: "Дополнительные данные отчета о закрытии ФН", Description: "Optional closure report data as hexadecimal text, at most 32 decoded bytes. Supply together with reportAttribute.", JSONType: "string", WireType: "HEX", Tag: 1283, MaxLength: 32, RequiredIf: "reportAttribute is present"},
			},
			Preconditions: []string{"A fiscal-storage closure report has been started with 0x14 and has not yet been finalized with 0x28."},
			Validate: func(params map[string]json.RawMessage) error {
				_, attribute := params["reportAttribute"]
				_, data := params["reportDataHex"]
				if attribute != data {
					return fmt.Errorf("reportAttribute and reportDataHex must be supplied together")
				}
				return nil
			},
		},
		{
			Code: 0x28, Name: "finalizeFiscalStorageClosure", TitleRU: "Сформировать отчет о закрытии ФН", Category: "registration", Page: 13,
			Description:   "Finalize the fiscal-storage closure report started with 0x14. The eight-byte response contains two little-endian UINT32 values: fiscal document number and fiscal sign.",
			DescriptionRU: "Сформировать отчет о закрытии ФН (0x28). Входные параметры отсутствуют. Выходные данные: номер ФД, UINT32 LE; фискальный признак, UINT32 LE. Команда завершает формирование отчета о закрытии ФН, начатое командой 0x14. Контекст отличается от отчета о состоянии расчетов, начатого командой 0x27.",
			Output:        registrationFiscalResult(),
			Preconditions: []string{"A fiscal-storage closure report must have been started with 0x14 in the same complete operation."},
			Notes:         []string{"Wire byte 0x28 is selected by the active report context: closure after 0x14 uses this eight-byte response, while settlement after 0x27 uses its own fifteen-byte response.", "The canonical closure command has no numeric aliases; numeric 0x28 is resolved by the complete operation's active report context."},
		},
	}
	commands := make([]Command, 0, len(specs))
	for _, spec := range specs {
		command := makeCommand(spec)
		command.Definition.Source = ffd12ExtraSource(13)
		command.Definition.ProtocolProfile = "ffd1.2"
		command.Definition.OperationRequired = true
		if spec.Code == 0x28 {
			command.Definition.Aliases = []string{}
		}
		commands = append(commands, command)
	}
	return commands
}

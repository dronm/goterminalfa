package service

func ffd12DiagnosticCommands() []Command {
	activeFormat := informationByte("ffdVersion", "Код формата ФФД, используемый в ФН", "Active fiscal data format selected when the fiscal storage was activated; 0 means not activated.")
	activeFormat.Enum = map[string]string{
		"0": "ФН не активизирован", "1": "ФФД 1.0 (1.05)", "3": "ФФД 1.1", "4": "ФФД 1.2",
	}
	supportedFormat := informationByte("supportedFfdVersion", "Код версии ФФД, которую может поддерживать ФН", "Maximum fiscal data format supported by the fiscal storage; the supplement specifies 4 for an FN 1.2. Unrecognized firmware values remain available as raw numeric codes.")
	state := informationByte("state", "Состояние передачи уведомлений", "OISM notification exchange state; unknown firmware values are preserved.")
	state.Enum = map[string]string{"0": "Нет активного обмена", "1": "Начато чтение уведомления", "2": "Ожидание квитанции на уведомление"}
	storage := informationByte("notificationStorageUsedPercent", "Ресурс для хранения уведомлений о реализации маркированного товара", "Percentage of notification storage used; absent when the FN is registered without marked-goods support.")
	storage.Required = false
	storage.Unit = "percent"
	storage.RequiredIf = "Returned only when the fiscal storage is registered with marked-goods support"
	specs := []commandSpec{
		{
			Code: 0x0F, Name: "getFiscalStorageFormat", Category: "diagnostics", Page: 5, ReadOnly: true,
			TitleRU:       "Запрос формата ФФД, используемого в ФН",
			Description:   "Read the active and supported fiscal data format codes as two raw bytes. The supplement documents supported code 4 for FN 1.2; unknown future response codes are preserved.",
			DescriptionRU: "Запрос формата ФФД, используемого в ФН (0x0F). Входные параметры отсутствуют. Ответ: код используемого формата ФФД, BYTE (0 — ФН не активизирован, 1 — ФФД 1.0/1.05, 3 — ФФД 1.1, 4 — ФФД 1.2); код поддерживаемой версии ФФД, BYTE (для ФН 1.2 — 4).",
			Output:        []ParameterDefinition{activeFormat, supportedFormat},
			Notes:         []string{"The response is exactly two bytes. Response values outside the documented dictionary are preserved for newer firmware."},
		},
		{
			Code: 0x9E, Name: "getOismNotificationStatus", Category: "diagnostics", Page: 5, ReadOnly: true,
			TitleRU:       "Запрос состояния передачи уведомлений ОИСМ",
			Description:   "Read OISM notification exchange state, queue size, current notification number and local date/time, and notification storage usage. An all-zero date is returned as null.",
			DescriptionRU: "Запрос состояния передачи уведомлений ОИСМ (0x9E). Входные параметры отсутствуют. Ответ: состояние передачи BYTE, количество уведомлений в очереди UINT16 LE, номер текущего уведомления UINT32 LE, дата и время текущего уведомления YMDHm (5 байт), процент заполнения области хранения уведомлений BYTE. Нулевая дата означает, что на все уведомления получены квитанции.",
			Output: []ParameterDefinition{
				state,
				{Name: "queuedNotifications", TitleRU: "Количество уведомлений в очереди", Description: "Notifications awaiting acknowledgement; zero when all notifications have acknowledgements.", JSONType: "integer", WireType: "UINT16", Required: true},
				{Name: "currentNotificationNumber", TitleRU: "Номер текущего уведомления", Description: "Number of the notification being sent or awaiting acknowledgement.", JSONType: "integer", WireType: "UINT32", Required: true},
				{Name: "currentNotificationDateTime", TitleRU: "Дата и время текущего уведомления", Description: "Device-local YMDHm calendar value, or null when all five date bytes are zero; no timezone conversion is applied.", JSONType: "string", WireType: "DATETIME5", Required: true, AllowZeroDate: true},
				{Name: "storageUsedPercent", TitleRU: "Процент заполнения области хранения уведомлений", Description: "Percentage of notification storage used, returned as the device's raw byte value.", JSONType: "integer", WireType: "BYTE", Required: true, Unit: "percent"},
			},
			Notes: []string{"The response is exactly thirteen bytes. Unrecognized state values are preserved as raw numeric codes."},
		},
		{
			Code: 0x9D, Name: "getFiscalStorageMemory", Category: "diagnostics", Page: 6, ReadOnly: true,
			TitleRU:       "Запрос ресурса свободной памяти в ФН",
			Description:   "Read the estimated remaining five-year documents and free thirty-day storage in kilobytes. The optional ninth response byte reports marked-goods notification storage usage.",
			DescriptionRU: "Запрос ресурса свободной памяти в ФН (0x9D). Входные параметры отсутствуют. Ответ: ориентировочное количество документов 5-летнего хранения UINT32 LE; свободная область данных 30-дневного хранения в килобайтах UINT32 LE. Необязательный завершающий BYTE — процент заполнения области хранения уведомлений ОИСМ; отсутствует при регистрации ФН без поддержки маркированных товаров.",
			Output: []ParameterDefinition{
				{Name: "remainingLongTermDocuments", TitleRU: "Ресурс данных 5-летнего хранения", Description: "Estimated number of five-year documents that can still be created in the fiscal storage.", JSONType: "integer", WireType: "UINT32", Required: true, Unit: "documents"},
				{Name: "freeShortTermStorageKB", TitleRU: "Ресурс данных 30-дневного хранения", Description: "Free storage for thirty-day documents, in kilobytes.", JSONType: "integer", WireType: "UINT32", Required: true, Unit: "KB"},
				storage,
			},
			Notes: []string{"The response is exactly eight or nine bytes. An omitted notification usage byte remains absent from the JSON response; it is not replaced with zero."},
		},
		{
			Code: 0xA2, Name: "getServiceParameters", Category: "diagnostics", Page: 28, ReadOnly: true,
			TitleRU:       "Запрос служебных параметров ККТ",
			Description:   "Read the KKT service parameters as an opaque CP866 string. Preserve the complete response text, including whitespace and an empty value; the supplement defines no internal field format.",
			DescriptionRU: "Запрос служебных параметров ККТ (0xA2). Входные параметры отсутствуют. Выходные данные: строка символов ASCII[N]. Содержание строки не интерпретируется драйвером.",
			Output:        []ParameterDefinition{informationText("parameters", "Служебные параметры ККТ", "Opaque CP866 service-parameter text; no undocumented splitting, trimming, or interpretation is performed.")},
		},
	}
	commands := make([]Command, 0, len(specs))
	for _, spec := range specs {
		command := makeCommand(spec)
		command.Definition.Source = ffd12ExtraSource(spec.Page)
		commands = append(commands, command)
	}
	return commands
}

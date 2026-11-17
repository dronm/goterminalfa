package service

// ffd12HostCommands contains the supplement's OISM and marking-key host settings.
// These settings are independent of the transport used to control the KKT.
func ffd12HostCommands() []Command {
	oismFields := []ParameterDefinition{
		ffd12HostField(30050, "Адрес хоста ОИСМ"),
		ffd12HostPortField(30051, "TCP-порт хоста ОИСМ"),
		{Name: "timeoutSeconds", TitleRU: "Таймаут ожидания ответа от хоста ОИСМ", Description: "Response timeout in seconds. The recommended value 30 is not an implicit default.", JSONType: "integer", WireType: "UINT16", Tag: 30052, Required: true, Unit: "seconds"},
	}
	keyFields := []ParameterDefinition{
		ffd12HostField(30060, "Адрес хоста обновления ключей проверки КМ"),
		ffd12HostPortField(30061, "TCP-порт хоста обновления ключей проверки КМ"),
	}
	specs := []commandSpec{
		{
			Code: 0x82, Name: "setOismSettings", TitleRU: "Установить настройки взаимодействия с хостом ОИСМ", Category: "settings", Page: 4, RequestLayout: "tlv",
			Description:   "Set all documented OISM host settings: CP866 host text, TCP port, and response timeout in seconds. The recommended 30-second timeout must be supplied explicitly if desired.",
			DescriptionRU: "Установить настройки взаимодействия с хостом ОИСМ (0x82). Адрес хоста: TLV, тег 30050, ASCII, не более 128 символов. TCP-порт: тег 30051, UINT16 LE. Таймаут ожидания ответа в секундах: тег 30052, UINT16 LE; рекомендуется 30 секунд. Все параметры передаются явно. Выходные данные: нет.",
			Input:         oismFields,
			Notes:         []string{"Before FFD 1.2 registration, save both the OISM host and marking-verification key-update host settings as required on supplement page 5.", "The supplement specifies no host syntax or positive minimum host length, port, or timeout. No values are inferred."},
		},
		{
			Code: 0x83, Name: "getOismSettings", TitleRU: "Запрос настроек взаимодействия с хостом ОИСМ из ККТ", Category: "settings", Page: 4, ReadOnly: true, ResponseLayout: "tlv",
			Description:   "Read OISM host settings as TLVs: CP866 host text, TCP port, and response timeout in seconds. An empty configured host is preserved; unknown and repeated unknown tags are retained in order.",
			DescriptionRU: "Запрос настроек взаимодействия с хостом ОИСМ из ККТ (0x83). Входные параметры: нет. Выходные TLV: адрес хоста, тег 30050, ASCII, не более 128 символов; TCP-порт, тег 30051, UINT16 LE; таймаут ожидания ответа в секундах, тег 30052, UINT16 LE.",
			Output:        oismFields,
		},
		{
			Code: 0x84, Name: "setMarkingKeyUpdateSettings", TitleRU: "Установить хост обновления ключей проверки КМ", Category: "settings", Page: 4, RequestLayout: "tlv",
			Description:   "Set the marking-verification key-update host and TCP port. Both documented fields must be supplied; no host or port is inferred.",
			DescriptionRU: "Установить хост обновления ключей проверки КМ (0x84). Входные TLV: адрес хоста, тег 30060, ASCII, не более 128 символов; TCP-порт, тег 30061, UINT16 LE. Оба параметра обязательны. Выходные данные: нет.",
			Input:         keyFields,
			Notes:         []string{"Relevant to KKT registered for marked-goods sales in data-transfer mode when the FN cannot store the marking-verification key-update host address (supplement page 5).", "Before FFD 1.2 registration, save both the OISM host and marking-verification key-update host settings.", "The supplement specifies no host syntax or positive minimum host length or port."},
		},
		{
			Code: 0x85, Name: "getMarkingKeyUpdateSettings", TitleRU: "Запрос хоста обновления ключей проверки КМ из ККТ", Category: "settings", Page: 4, ReadOnly: true, ResponseLayout: "tlv",
			Description:   "Read the marking-verification key-update host and TCP port as TLVs. An empty configured host is preserved; unknown and repeated unknown tags are retained in order.",
			DescriptionRU: "Запрос хоста обновления ключей проверки КМ из ККТ (0x85). Входные параметры: нет. Выходные TLV: адрес хоста, тег 30060, ASCII, не более 128 символов; TCP-порт, тег 30061, UINT16 LE.",
			Output:        keyFields,
		},
	}
	commands := make([]Command, 0, len(specs))
	for _, spec := range specs {
		command := makeCommand(spec)
		command.Definition.Source = ffd12ExtraSource(4)
		commands = append(commands, command)
	}
	return commands
}

func ffd12HostField(tag uint16, title string) ParameterDefinition {
	return ParameterDefinition{Name: "host", TitleRU: title, Description: "Configured host text, at most 128 CP866 bytes. No host syntax or positive minimum length is specified.", JSONType: "string", WireType: "ASCII", Encoding: "CP866", Tag: tag, Required: true, MaxLength: 128}
}

func ffd12HostPortField(tag uint16, title string) ParameterDefinition {
	return ParameterDefinition{Name: "tcpPort", TitleRU: title, Description: "TCP port represented as a little-endian UINT16; no positive minimum is specified.", JSONType: "integer", WireType: "UINT16", Tag: tag, Required: true}
}

package service

import (
	"encoding/json"
	"strings"
)

func ffd12ActivationCommand() Command {
	command := makeCommand(commandSpec{
		Code: 0xDD, Name: "applyActivationCode", Category: "activation", Page: 28,
		TitleRU:       "Ввод кода активации для команд ФФД 1.2",
		Description:   "Apply the device-specific FFD 1.2 activation code. Exactly 16 decoded bytes are transmitted without a TLV wrapper; a successful response has no data.",
		DescriptionRU: "Ввод кода активации для команд ФФД 1.2. Код должен быть действителен для конкретного экземпляра ККТ. Вход: массив из 16 байт без TLV-оболочки; в JSON передаются 32 шестнадцатеричных символа. Выходные данные отсутствуют.",
		Input: []ParameterDefinition{
			{Name: "activationCodeHex", TitleRU: "Код активации", Description: "Exactly 32 hexadecimal digits representing 16 raw bytes. Uppercase and lowercase are accepted; prefixes, spaces and separators are not accepted.", JSONType: "string", WireType: "HEX", FixedLength: 16, Required: true},
		},
		Notes: []string{
			"The code must be valid for this individual KKT; the driver does not generate activation codes or change fiscal registration.",
			"Hexadecimal letter case is normalized before journaling, so equivalent representations reuse the same requestId without another device write.",
		},
	})
	command.Definition.Source = ffd12ExtraSource(28)
	command.Canonicalize = func(raw json.RawMessage) (json.RawMessage, error) {
		if err := command.Validate(raw); err != nil {
			return nil, err
		}
		var params struct {
			ActivationCodeHex string `json:"activationCodeHex"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		params.ActivationCodeHex = strings.ToLower(params.ActivationCodeHex)
		return json.Marshal(params)
	}
	return command
}

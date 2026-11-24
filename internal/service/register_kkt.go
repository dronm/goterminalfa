package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
)

type preparedRegistration struct {
	steps                       []operationStep
	userInn, registrationNumber string
}

// registerKktCommand keeps the registration wire commands private. Every entry
// point to registration must validate the RN against this KKT before sending E1.
func registerKktCommand() Command {
	commands := ffd12RegistrationCommands()
	input := make([]ParameterDefinition, 0)
	for _, cmd := range commands {
		input = append(input, cmd.Definition.Input...)
	}
	parse := func(raw json.RawMessage) (preparedRegistration, error) {
		return prepareRegistration(raw, commands)
	}
	return Command{
		Definition: CommandDefinition{
			Name: "registerKkt", Aliases: []string{}, ProtocolProfile: "ffd1.2", Category: "registration",
			TitleRU:       "Зарегистрировать или перерегистрировать ККТ",
			Description:   "Validate registration data, verify the RN KKT checksum against the actual factory serial read with 0x02, then execute 0xE1, 0xE2 and 0xE3 under one device lock and durable requestId. Returns the fiscal document number and sign. Stops at the first failure; no automatic cancellation or rollback.",
			DescriptionRU: "Проверить регистрационные данные и контрольную сумму РН ККТ по фактическому заводскому номеру ККТ, затем выполнить команды 0xE1, 0xE2, 0xE3 в рамках одной операции с общим requestId. При ошибке выполнение прекращается. Автоматическая отмена или откат не выполняются.",
			ReadOnly:      false, IdempotencyRequired: true, Implemented: true,
			RequestLayout: "application", ResponseLayout: "application",
			Input: input, Output: registrationFiscalResult(),
			Notes: []string{
				"FFD 1.2 only. Submit directly; registerKkt cannot be nested in executeOperation. The internal E1/E2/E3 commands are not exposed separately.",
				"Validate all fields and all three encoded payloads before communicating. The RN checksum is then checked against the actual KKT serial before E1; the FN serial is not used.",
				"The API accepts a 16-digit registrationNumber. E3 right-pads it with four ASCII spaces; INNs are right-padded with spaces to twelve bytes.",
				"reregistrationReason is a UINT32 LE bitmask, omitted for reportType 0 and required for reportType 1 or 2. Bit 31 is 2147483648.",
				"Tags 1203, 1036, 1274, 1275, 9998 and 9999 are optional. Preserve absent flags; explicitly supplied zero values are transmitted. Tags 1274 and 1275 must be supplied together.",
				"After any successful mutating step, a later failure leaves an unknown outcome and blocks automatic repetition. Replay the same requestId to retrieve its recorded result.",
			},
			Source: Source{Document: "goterminalfa HTTP API / Terminal FA FFD 1.2 supplement", Version: "0.7.6", Page: 7},
		},
		OwnsOperation: true,
		Canonicalize: func(raw json.RawMessage) (json.RawMessage, error) {
			if _, err := parse(raw); err != nil {
				return nil, err
			}
			object, err := operationObject(raw)
			if err != nil {
				return nil, err
			}
			// Hexadecimal letter case does not change the report bytes or the
			// identity of the intended operation.
			if value, present := object["reportDataHex"]; present {
				var encoded string
				if err := json.Unmarshal(value, &encoded); err != nil {
					return nil, err
				}
				data, err := hex.DecodeString(encoded)
				if err != nil {
					return nil, err
				}
				object["reportDataHex"], _ = json.Marshal(hex.EncodeToString(data))
			}
			return json.Marshal(object)
		},
		Validate: func(raw json.RawMessage) error {
			_, err := parse(raw)
			return err
		},
		Execute: func(ctx context.Context, device *Device, raw json.RawMessage) (any, error) {
			prepared, err := parse(raw)
			if err != nil {
				return nil, &NotExecutedError{Cause: &requestValidationError{Cause: err}}
			}
			// executeSafely already read and verified the configured serial under
			// this same session. Otherwise read the actual identity here.
			serial := device.ExpectedSerialNumber
			if serial == "" {
				serial, err = device.Client.SerialNumber(ctx)
				if err != nil {
					return nil, &NotExecutedError{Cause: err}
				}
			}
			if err := validateRegistrationNumber(prepared.registrationNumber, prepared.userInn, serial); err != nil {
				return nil, &NotExecutedError{Cause: &requestValidationError{Cause: err}}
			}
			results, err := runOperation(ctx, device, prepared.steps)
			if err != nil {
				return nil, err
			}
			// runOperation retains the shared mutation/uncertainty rules; the
			// external registration command returns just the final fiscal data.
			return results[len(results)-1].Data, nil
		},
	}
}

func prepareRegistration(raw json.RawMessage, commands []Command) (preparedRegistration, error) {
	var result preparedRegistration
	object, err := operationObject(raw)
	if err != nil {
		return result, err
	}
	result.steps, err = prepareFlatOperationSteps(object, commands)
	if err != nil {
		return result, err
	}
	reportType, _ := operationUintParam(raw, "reportType")
	_, hasReason := object["reregistrationReason"]
	if reportType == 0 && hasReason {
		return result, errors.New("initial registration must omit reregistrationReason")
	}
	if reportType != 0 && !hasReason {
		return result, errors.New("reregistration requires reregistrationReason")
	}
	_ = json.Unmarshal(object["userInn"], &result.userInn)
	_ = json.Unmarshal(object["registrationNumber"], &result.registrationNumber)
	return result, nil
}

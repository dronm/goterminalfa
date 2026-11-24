package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/dronm/goterminalfa/internal/protocol"
)

func closeShiftIfOpenCommand(byCode map[byte]Command) Command {
	status, start, cashier, finish := byCode[0x20], byCode[0x29], byCode[0x2F], byCode[0x2A]
	closing := []Command{start, cashier, finish}
	prepare := func(raw json.RawMessage) ([]operationStep, error) {
		params, err := operationObject(raw)
		if err != nil {
			return nil, err
		}
		return prepareFlatOperationSteps(params, closing)
	}
	output := []ParameterDefinition{{
		Name: "alreadyClosed", TitleRU: "Смена уже закрыта", JSONType: "boolean", Required: true,
		Description: "True when the status query found a closed shift.",
	}}
	for _, field := range finish.Definition.Output {
		field.Required = field.Name == "shiftNumber"
		output = append(output, field)
	}
	return Command{
		Definition: CommandDefinition{
			Name: "closeShiftIfOpen", ProtocolProfile: status.Definition.ProtocolProfile, Aliases: []string{}, Category: "shift",
			TitleRU:       "Закрыть смену, если она открыта",
			Description:   "Read shift state (0x20). If open, start closure (0x29), send cashier data (0x2F), and finalize the report (0x2A) under the same device lock.",
			DescriptionRU: "Проверить состояние смены (0x20). Если смена открыта, начать закрытие (0x29), передать данные кассира (0x2F) и сформировать отчет (0x2A) за одну операцию.",
			Implemented:   true, IdempotencyRequired: true, RequestLayout: "application", ResponseLayout: "application",
			Input: slices.Concat(cashier.Definition.Input, start.Definition.Input), Output: output,
			Notes: []string{
				"Submit directly with one requestId. A repeat with the same parameters retrieves the journaled outcome.",
				"Both success branches return alreadyClosed and shiftNumber. A newly closed shift also returns the fiscal report fields from the configured profile.",
			},
			Source: Source{Document: "goterminalfa HTTP API", Version: "0.7.7"},
		},
		OwnsOperation: true,
		Validate: func(raw json.RawMessage) error {
			_, err := prepare(raw)
			return err
		},
		Execute: func(ctx context.Context, device *Device, raw json.RawMessage) (any, error) {
			steps, err := prepare(raw)
			if err != nil {
				return nil, &NotExecutedError{Cause: &requestValidationError{Cause: err}}
			}
			data, err := status.Execute(ctx, device, json.RawMessage(`{}`))
			if err != nil {
				return nil, &NotExecutedError{Cause: err}
			}
			shift := data.(map[string]any)
			switch state := shift["shiftState"]; state {
			case uint64(0):
				return map[string]any{"alreadyClosed": true, "shiftNumber": shift["shiftNumber"]}, nil
			case uint64(1):
				results, err := runOperation(ctx, device, steps)
				if err != nil {
					return nil, err
				}
				report := results[len(results)-1].Data.(map[string]any)
				report["alreadyClosed"] = false
				return report, nil
			default:
				return nil, &NotExecutedError{Cause: fmt.Errorf("%w: getShiftParameters returned unsupported shift state %v", protocol.ErrMalformedFrame, state)}
			}
		},
	}
}

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type clockSyncResult struct {
	DateTime string `json:"dateTime"`
}

// syncDateTimeCommand delegates to the standard setter so encoding, response
// validation, session ownership, and operation mutation tracking stay shared.
func syncDateTimeCommand(setDateTime Command, now func() time.Time) Command {
	return Command{
		Definition: CommandDefinition{
			Name: "syncDateTime", Aliases: []string{}, Category: "settings",
			TitleRU:       "Синхронизировать время ККТ с сервером",
			Description:   "Set the terminal clock from the Go server's local date and time at execution using setDateTime (0x72, TLV tag 30000). Precision is one minute; seconds and timezone are not transmitted. This application command has no protocol command byte.",
			DescriptionRU: "Считать локальные дату и время сервера при выполнении и передать в ККТ стандартной командой 0x72. Точность — одна минута. Входных параметров нет. Возвращается переданное значение dateTime. Повтор requestId возвращает сохраненный результат без повторной синхронизации.",
			ReadOnly:      false, IdempotencyRequired: true, Implemented: true,
			RequestLayout: "application", ResponseLayout: "application",
			Input: []ParameterDefinition{},
			Output: []ParameterDefinition{{Name: "dateTime", TitleRU: "Установленные дата и время", JSONType: "string", Required: true,
				Description: "Server-local calendar date and time sent to the terminal, formatted YYYY-MM-DDTHH:mm; not a read-back of the terminal clock."}},
			Notes: []string{
				"The clock is sampled after queue waiting and configured identity verification, immediately before the standard setter is executed.",
				"The server process's local timezone is used, not the HTTP caller's timezone. Configure the host timezone or TZ environment variable as appropriate.",
				"Use a new requestId for each intended synchronization. Repeating an existing requestId replays its saved result without reading the clock again.",
			},
			Source: Source{Document: "goterminalfa HTTP API", Version: "0.7.0"},
		},
		Validate: func(params json.RawMessage) error {
			_, err := encodeFields(nil, "positional", params)
			return err
		},
		Execute: func(ctx context.Context, device *Device, _ json.RawMessage) (any, error) {
			if err := ctx.Err(); err != nil {
				return nil, &NotExecutedError{Cause: err}
			}
			// Format wall-clock fields directly: the protocol carries no timezone
			// or seconds. Do not truncate the absolute instant or convert to UTC.
			result := clockSyncResult{DateTime: now().Format("2006-01-02T15:04")}
			params, err := json.Marshal(result)
			if err != nil {
				return nil, &NotExecutedError{Cause: err}
			}
			if err := setDateTime.Validate(params); err != nil {
				return nil, &NotExecutedError{Cause: fmt.Errorf("server clock cannot be represented by setDateTime: %w", err)}
			}
			if _, err := setDateTime.Execute(ctx, device, params); err != nil {
				return nil, err
			}
			return result, nil
		},
	}
}

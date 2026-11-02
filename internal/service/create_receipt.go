package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	terminalfa "github.com/dronm/goterminalfa"
	"github.com/dronm/goterminalfa/internal/protocol"
)

const maxReceiptItems = 200

type receiptCleanup struct {
	Attempted     bool   `json:"attempted"`
	Succeeded     bool   `json:"succeeded"`
	Error         *Error `json:"error,omitempty"`
	SkippedReason string `json:"skippedReason,omitempty"`
}

type createReceiptResult struct {
	ShiftOpenedByRequest bool            `json:"shiftOpenedByRequest"`
	ShiftResult          any             `json:"shiftResult,omitempty"`
	Receipt              any             `json:"receipt,omitempty"`
	FailedStage          string          `json:"failedStage,omitempty"`
	FailedCommand        string          `json:"failedCommand,omitempty"`
	FailedItemIndex      *int            `json:"failedItemIndex,omitempty"`
	Failure              *Error          `json:"failure,omitempty"`
	Cleanup              *receiptCleanup `json:"cleanup,omitempty"`

	identity          string
	documentOwned     bool
	mutationAttempted bool
	completed         bool
}

// Do not unwrap: failed recovery must not expose a preceding DeviceError as
// evidence that the whole operation's fiscal outcome is known.
type createReceiptError struct {
	Cause         error
	Unknown       bool
	Result        *createReceiptResult
	Step, Command string
}

func (e *createReceiptError) Error() string {
	return fmt.Sprintf("createReceipt failed at %s (%s): %v", e.Step, e.Command, e.Cause)
}

type preparedReceipt struct {
	shiftStart, cashier, receiptStart, finalize json.RawMessage
	items                                       []json.RawMessage
	additional, payment                         json.RawMessage
}

func createReceiptCommand(commands []Command) Command {
	lookup := make(map[byte]Command)
	for _, cmd := range commands {
		if code, ok := operationProtocolCode(cmd.Definition.ProtocolCode); ok {
			lookup[code] = cmd
		}
	}
	parse := func(raw json.RawMessage) (preparedReceipt, error) { return prepareReceipt(raw, lookup) }
	shiftPrint := registrationSuppressPrint()
	shiftPrint.Name = "suppressShiftPrint"
	return Command{
		Definition: CommandDefinition{
			Name: "createReceipt", Aliases: []string{}, ProtocolProfile: "ffd1.2", Category: "operations",
			TitleRU:       "Создать чек с автоматическим открытием смены",
			Description:   "Check shift state, open it with 0x21/0x2F/0x22 when closed, and create an unmarked FFD 1.2 receipt with 0x23, one 0x96 per item, optional additional/payment data, and 0x24. On error attempt 0x10 only for a document confirmed opened by this operation. One device gate and requestId cover execution and recovery.",
			DescriptionRU: "Проверить смену 0x20, при необходимости открыть ее командами 0x21, 0x2F, 0x22, затем сформировать чек командами 0x23, 0x96, при наличии данных 0x98 и 0x2D, и 0x24. При ошибке отменить документ 0x10 только если его открытие подтверждено в данной операции. Неопределенный результат фискализации не считается отмененным после успешной команды 0x10.",
			ReadOnly:      false, IdempotencyRequired: true, Implemented: true, RequestLayout: "application", ResponseLayout: "application",
			Input: []ParameterDefinition{
				lookup[0x2F].Definition.Input[0], lookup[0x2F].Definition.Input[1], shiftPrint, lookup[0x23].Definition.Input[0],
				{Name: "items", JSONType: "array", WireType: "APPLICATION", Required: true, MinItems: 1, MaxItems: maxReceiptItems, Description: "Unmarked items using the flat 0x96 schema.", Children: lookup[0x96].Definition.Input},
				{Name: "additionalData", JSONType: "object", WireType: "APPLICATION", Description: "Optional additional receipt attributes sent with 0x98.", Children: lookup[0x98].Definition.Input},
				{Name: "payment", JSONType: "object", WireType: "APPLICATION", Description: "Optional explicit payment data for 0x2D. Omission sends no payment command; amounts are not inferred.", Children: lookup[0x2D].Definition.Input},
				lookup[0x24].Definition.Input[0], lookup[0x24].Definition.Input[1],
			},
			Output: []ParameterDefinition{
				{Name: "shiftOpenedByRequest", JSONType: "boolean", Required: true, Description: "True only after confirmed successful 0x22. The shift remains open if receipt creation subsequently fails."},
				{Name: "shiftResult", JSONType: "object", Children: lookup[0x22].Definition.Output, Description: "Shift-opening fiscal result when this operation opened the shift."},
				{Name: "receipt", JSONType: "object", Children: lookup[0x24].Definition.Output, Description: "Confirmed receipt fiscal result."},
				{Name: "failedStage", JSONType: "string", Description: "Canonical command name of the failed stage."},
				{Name: "failedCommand", JSONType: "string", Description: "Failed protocol byte as a hexadecimal string."},
				{Name: "failedItemIndex", JSONType: "integer", Description: "Zero-based item index when an item failed."},
				{Name: "failure", JSONType: "object", Description: "Original failure in the standard error format, retained even when cleanup leaves an unknown outcome."},
				{Name: "cleanup", JSONType: "object", Description: "On failure: attempted, succeeded, optional error and skippedReason."},
			},
			Notes: []string{
				"FFD 1.2 only. Submit directly, not inside executeOperation. Validate all items and both shift branches before communication.",
				"Cashier name is required even if the shift is already open: the conditional branch is chosen only at execution time.",
				"Recovery closes the failed session, then uses a fresh session with a separate 25-second deadline and verifies the same factory serial before cancellation. The device gate remains held.",
				"An unconfirmed opening or rejected begin command does not authorize canceling a potentially preexisting document. Lost finalization remains unknown even if cancellation succeeds.",
			},
			Source: Source{Document: "goterminalfa HTTP API", Version: "0.7.0"},
		},
		Validate: func(raw json.RawMessage) error { _, err := parse(raw); return err },
		Execute: func(ctx context.Context, device *Device, raw json.RawMessage) (data any, err error) {
			r := &createReceiptResult{identity: device.ExpectedSerialNumber}
			data = r
			defer func() {
				if recovered := recover(); recovered != nil {
					err = &createReceiptError{Cause: fmt.Errorf("receipt handler panic: %v", recovered), Unknown: true, Result: r, Step: r.FailedStage, Command: r.FailedCommand}
				}
			}()
			p, err := parse(raw)
			if err != nil {
				return r, &createReceiptError{Cause: &NotExecutedError{Cause: err}, Result: r, Step: "validation"}
			}
			fail := func(cause error) (any, error) {
				return r, &createReceiptError{Cause: cause, Result: r, Unknown: r.mutationAttempted && !definiteReceiptFailure(cause), Step: r.FailedStage, Command: r.FailedCommand}
			}
			// Generic execution checked the configured expected serial. Otherwise
			// capture the actual identity to bind recovery to this same unit.
			if r.identity == "" {
				r.FailedStage, r.FailedCommand = "getSerialNumber", "0x02"
				r.identity, err = device.Client.SerialNumber(ctx)
				if err != nil {
					return fail(err)
				}
			}
			run := func(code byte, params json.RawMessage) (any, error) {
				cmd := lookup[code]
				r.FailedStage, r.FailedCommand = cmd.Definition.Name, cmd.Definition.ProtocolCode
				if !cmd.Definition.ReadOnly {
					r.mutationAttempted = true
				}
				return cmd.Execute(ctx, device, params)
			}
			shift, err := run(0x20, json.RawMessage(`{}`))
			if err != nil {
				return fail(err)
			}
			values, ok := shift.(map[string]any)
			if !ok {
				return fail(fmt.Errorf("%w: shift response is not an object", protocol.ErrMalformedFrame))
			}
			state, ok := values["shiftState"].(uint64)
			if !ok || state > 1 {
				return fail(fmt.Errorf("%w: unsupported shift state", protocol.ErrMalformedFrame))
			}
			if state == 0 {
				if _, err := run(0x21, p.shiftStart); err != nil {
					return fail(err)
				}
				r.documentOwned = true
				if _, err := run(0x2F, p.cashier); err != nil {
					return fail(err)
				}
				r.ShiftResult, err = run(0x22, json.RawMessage(`{}`))
				if err != nil {
					return fail(err)
				}
				r.documentOwned, r.ShiftOpenedByRequest = false, true
			}
			if _, err := run(0x23, p.receiptStart); err != nil {
				return fail(err)
			}
			r.documentOwned = true
			for i, item := range p.items {
				if _, err := run(0x96, item); err != nil {
					index := i
					r.FailedItemIndex = &index
					return fail(err)
				}
			}
			for _, step := range []struct {
				code byte
				raw  json.RawMessage
			}{{0x98, p.additional}, {0x2D, p.payment}} {
				if step.raw != nil {
					if _, err := run(step.code, step.raw); err != nil {
						return fail(err)
					}
				}
			}
			r.Receipt, err = run(0x24, p.finalize)
			if err != nil {
				return fail(err)
			}
			r.documentOwned, r.completed = false, true
			r.FailedStage, r.FailedCommand = "", ""
			return r, nil
		},
		OnError: func(ctx context.Context, original *Device, data any, cause error) (any, error) {
			r, ok := data.(*createReceiptResult)
			if !ok {
				return data, cause
			}
			if r.completed {
				return r, nil
			}
			var failure *createReceiptError
			if !errors.As(cause, &failure) {
				failure = &createReceiptError{Cause: cause, Unknown: true, Result: r, Step: r.FailedStage, Command: r.FailedCommand}
			}
			var primaryResponse Response
			_ = json.Unmarshal(executionFailure(Request{}, failure.Cause).Body, &primaryResponse)
			r.Failure = primaryResponse.Error
			r.Cleanup = &receiptCleanup{}
			if !r.documentOwned {
				r.Cleanup.SkippedReason = "no_confirmed_document_opened_by_request"
				return r, failure
			}
			r.Cleanup.Attempted = true
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 25*time.Second)
			defer cancel()
			cleanupErr := original.Client.WithSession(cleanupCtx, func(client *terminalfa.Client) error {
				serial, err := client.SerialNumber(cleanupCtx)
				if err != nil {
					return err
				}
				if serial != r.identity {
					return &IdentityError{Expected: r.identity, Actual: serial}
				}
				scoped := *original
				scoped.Client = client
				_, err = lookup[0x10].Execute(cleanupCtx, &scoped, json.RawMessage(`{}`))
				return err
			})
			if cleanupErr != nil {
				failure.Unknown = true
				var response Response
				_ = json.Unmarshal(executionFailure(Request{}, cleanupErr).Body, &response)
				r.Cleanup.Error = response.Error
			} else {
				r.Cleanup.Succeeded = true
			}
			return r, failure
		},
	}
}

func definiteReceiptFailure(err error) bool {
	var rejection *protocol.DeviceError
	var notSent *terminalfa.CommandNotSentError
	var notExecuted *NotExecutedError
	return errors.As(err, &rejection) || errors.As(err, &notSent) || errors.As(err, &notExecuted)
}

func createReceiptFailure(req Request, failure *createReceiptError) Reply {
	if failure.Unknown {
		return JSONReply(409, Response{DeviceID: req.DeviceID, Command: req.Command, RequestID: req.RequestID, Status: "unknown", Data: failure.Result,
			Error: &Error{Code: "outcome_unknown", Message: "receipt or shift outcome may be unknown; cancellation does not undo a finalized fiscal document; reconcile before taking further action; automatic retry is blocked"}})
	}
	standard := executionFailure(req, failure.Cause)
	var response Response
	if err := json.Unmarshal(standard.Body, &response); err != nil {
		return standard
	}
	response.Data = failure.Result
	return JSONReply(standard.HTTPStatus, response)
}

func prepareReceipt(raw json.RawMessage, commands map[byte]Command) (preparedReceipt, error) {
	var p preparedReceipt
	object, err := operationObject(raw)
	if err != nil {
		return p, err
	}
	allowed := map[string]bool{"cashierName": true, "cashierInn": true, "suppressShiftPrint": true, "printSuppressionCode": true,
		"items": true, "additionalData": true, "payment": true, "calculationSign": true, "totalKopecks": true}
	for name := range object {
		if !allowed[name] {
			return p, fmt.Errorf("unknown createReceipt parameter %q", name)
		}
	}
	fields := func(names map[string]string) json.RawMessage {
		values := make(map[string]json.RawMessage)
		for from, to := range names {
			if value, present := object[from]; present {
				values[to] = value
			}
		}
		data, _ := json.Marshal(values)
		return data
	}
	p.cashier = fields(map[string]string{"cashierName": "cashierName", "cashierInn": "cashierInn"})
	p.shiftStart = fields(map[string]string{"suppressShiftPrint": "suppressPrint"})
	p.receiptStart = fields(map[string]string{"printSuppressionCode": "printSuppressionCode"})
	p.finalize = fields(map[string]string{"calculationSign": "calculationSign", "totalKopecks": "totalKopecks"})
	p.additional, p.payment = object["additionalData"], object["payment"]
	if err := json.Unmarshal(object["items"], &p.items); err != nil || len(p.items) < 1 || len(p.items) > maxReceiptItems {
		return p, fmt.Errorf("items must contain 1–%d unmarked item objects", maxReceiptItems)
	}
	for _, step := range []struct {
		code byte
		raw  json.RawMessage
	}{
		{0x20, json.RawMessage(`{}`)}, {0x21, p.shiftStart}, {0x2F, p.cashier}, {0x22, json.RawMessage(`{}`)},
		{0x23, p.receiptStart}, {0x24, p.finalize}, {0x10, json.RawMessage(`{}`)},
	} {
		if err := commands[step.code].Validate(step.raw); err != nil {
			return p, fmt.Errorf("%s: %w", commands[step.code].Definition.Name, err)
		}
	}
	for i, item := range p.items {
		if err := commands[0x96].Validate(item); err != nil {
			return p, fmt.Errorf("items[%d]: %w", i, err)
		}
	}
	for code, value := range map[byte]json.RawMessage{0x98: p.additional, 0x2D: p.payment} {
		if value != nil {
			if err := commands[code].Validate(value); err != nil {
				return p, fmt.Errorf("%s: %w", commands[code].Definition.Name, err)
			}
		}
	}
	return p, nil
}

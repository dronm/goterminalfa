package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	terminalfa "github.com/dronm/goterminalfa"
)

const (
	maxOperationCommands = 256
	maxArchiveReads      = 2048
	maxArchiveBytes      = 65535
)

type operationStep struct {
	Command             string          `json:"cmd"`
	Params              json.RawMessage `json:"params"`
	RepeatUntilComplete bool            `json:"repeatUntilComplete,omitempty"`
	resolved            Command
}

type operationResult struct {
	Command string `json:"cmd"`
	Data    any    `json:"data"`
}

// UncertainOperationError deliberately does not unwrap its cause. A definite
// rejection of a later step does not prove that preceding fiscal steps had no
// effect. Such a batch must retain its idempotency claim as an unknown outcome.
type UncertainOperationError struct {
	Step    int
	Command string
	Cause   error
}

func (e *UncertainOperationError) Error() string {
	return fmt.Sprintf("operation stopped at step %d (%s) after state-changing work may have executed: %v", e.Step, e.Command, e.Cause)
}

func operationCommand(commands []Command) Command {
	lookup := make(map[string]Command)
	for _, cmd := range commands {
		lookup[cmd.Definition.Name] = cmd
		for _, alias := range cmd.Definition.Aliases {
			lookup[alias] = cmd
		}
	}
	parse := func(raw json.RawMessage) ([]operationStep, error) {
		return parseOperation(raw, lookup)
	}
	return Command{
		Definition: CommandDefinition{
			Name: "executeOperation", Aliases: []string{}, Category: "operations",
			TitleRU:       "Выполнить последовательность команд одной операцией",
			Description:   "Execute a complete command sequence under one device lock and one durable requestId. Each command is validated before any device communication. Serial transport remains open for the complete operation; TCP uses one connection per protocol command. This application operation has no protocol command byte.",
			DescriptionRU: "Операция HTTP API драйвера, а не отдельная команда протокола ККТ. Все команды последовательности проверяются заранее и выполняются с исключительным доступом к одному устройству. При неопределенном исходе повторное выполнение блокируется.",
			ReadOnly:      false, IdempotencyRequired: true, Implemented: true,
			Input: []ParameterDefinition{{Name: "commands", JSONType: "array", WireType: "OPERATION", Required: true,
				Description: "Ordered commands; complete all started fiscal, archive, or print-buffer workflows in this request.", MinItems: 1, MaxItems: maxOperationCommands,
				Children: []ParameterDefinition{
					{Name: "cmd", JSONType: "string", Required: true, Description: "Implemented command name or alias."},
					{Name: "params", JSONType: "object", Description: "Parameters of the selected command; omission means an empty object."},
					{Name: "repeatUntilComplete", JSONType: "boolean", Description: "Only for archive command 0x36: explicitly read until the length returned by 0x35 has been consumed; at most 2048 reads."},
				}}},
			Output: []ParameterDefinition{{Name: "results", JSONType: "array", Required: true,
				Description: "Ordered command results. A repeated archive-read result contains chunks and byteCount."}},
			Source: Source{Document: "goterminalfa HTTP API", Version: "0.5.0"},
		},
		Canonicalize: func(raw json.RawMessage) (json.RawMessage, error) {
			steps, err := parse(raw)
			if err != nil {
				return nil, err
			}
			return json.Marshal(struct {
				Commands []operationStep `json:"commands"`
			}{Commands: steps})
		},
		Validate: func(raw json.RawMessage) error {
			_, err := parse(raw)
			return err
		},
		ValidateForDevice: func(device *Device, raw json.RawMessage) error {
			if device.ConnectionType != "tcp" {
				return nil
			}
			steps, err := parse(raw)
			if err != nil {
				return err
			}
			for i, step := range steps {
				code, _ := operationProtocolCode(step.resolved.Definition.ProtocolCode)
				if (code == 0x74 || code == 0x80) && i != len(steps)-1 {
					return errors.New("network or control settings must be the final command for a TCP-connected device; subsequent commands may require a different endpoint or transport")
				}
			}
			return nil
		},
		Execute: func(ctx context.Context, device *Device, raw json.RawMessage) (any, error) {
			steps, err := parse(raw)
			if err != nil {
				return nil, err
			}
			return runOperation(ctx, device, steps)
		},
	}
}

func parseOperation(raw json.RawMessage, lookup map[string]Command) ([]operationStep, error) {
	object, err := operationObject(raw)
	if err != nil {
		return nil, err
	}
	if len(object) != 1 || object["commands"] == nil {
		return nil, errors.New("executeOperation requires only a commands array")
	}
	var rawSteps []json.RawMessage
	if err := json.Unmarshal(object["commands"], &rawSteps); err != nil || len(rawSteps) < 1 || len(rawSteps) > maxOperationCommands {
		return nil, fmt.Errorf("commands must be an array containing 1–%d commands", maxOperationCommands)
	}
	steps := make([]operationStep, 0, len(rawSteps))
	for index, rawStep := range rawSteps {
		fields, err := operationObject(rawStep)
		if err != nil {
			return nil, fmt.Errorf("commands[%d]: %w", index, err)
		}
		for name := range fields {
			if name != "cmd" && name != "params" && name != "repeatUntilComplete" {
				return nil, fmt.Errorf("commands[%d]: unknown field %q", index, name)
			}
		}
		var step operationStep
		if err := json.Unmarshal(rawStep, &step); err != nil || step.Command == "" {
			return nil, fmt.Errorf("commands[%d] requires a command name", index)
		}
		if step.Command == "executeOperation" {
			return nil, errors.New("executeOperation cannot contain another executeOperation")
		}
		cmd, ok := lookup[step.Command]
		if !ok || !cmd.Definition.Implemented || cmd.Execute == nil || cmd.Validate == nil {
			return nil, fmt.Errorf("commands[%d]: command %q is unavailable", index, step.Command)
		}
		step.Command = cmd.Definition.Name
		step.resolved = cmd
		step.Params, err = canonicalParams(step.Params)
		if err != nil {
			return nil, fmt.Errorf("commands[%d] params: %w", index, err)
		}
		if cmd.Canonicalize != nil {
			step.Params, err = cmd.Canonicalize(step.Params)
			if err != nil {
				return nil, fmt.Errorf("commands[%d] params: %w", index, err)
			}
		}
		if err := cmd.Validate(step.Params); err != nil {
			return nil, fmt.Errorf("commands[%d] (%s): %w", index, step.Command, err)
		}
		code, hasCode := operationProtocolCode(cmd.Definition.ProtocolCode)
		if flag, present := fields["repeatUntilComplete"]; present {
			if bytes.Equal(bytes.TrimSpace(flag), []byte("null")) {
				return nil, errors.New("repeatUntilComplete must be a boolean")
			}
			if !hasCode || code != 0x36 {
				return nil, errors.New("repeatUntilComplete is allowed only for archive command 0x36")
			}
		}
		steps = append(steps, step)
	}
	if err := validateOperationWorkflow(steps); err != nil {
		return nil, err
	}
	return steps, nil
}

// operationObject is independent of envelope decoding because validation is
// also used by direct Go callers. It rejects duplicate keys recursively.
func operationObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := operationJSONValue(decoder, 0); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("expected exactly one JSON object")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, errors.New("expected a JSON object")
	}
	return object, nil
}

func operationJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("JSON nesting exceeds 64 levels")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := map[string]bool{}
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok || keys[key] {
				return errors.New("duplicate JSON object key")
			}
			keys[key] = true
			if err := operationJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := operationJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	_, err = decoder.Token()
	return err
}

func operationProtocolCode(value string) (byte, bool) {
	if !strings.HasPrefix(strings.ToLower(value), "0x") {
		return 0, false
	}
	n, err := strconv.ParseUint(value[2:], 16, 8)
	return byte(n), err == nil
}

func isWorkflowCommand(value string) bool {
	code, ok := operationProtocolCode(value)
	if !ok {
		return false
	}
	switch code {
	case 0x12, 0x13, 0x16, 0x19, 0x1F, 0x21, 0x22, 0x23, 0x24,
		0x25, 0x26, 0x27, 0x28, 0x29, 0x2A, 0x2B, 0x2C, 0x2D, 0x2E,
		0x2F, 0x35, 0x36, 0x3F, 0x64, 0x65:
		return true
	}
	return false
}

func validateOperationWorkflow(steps []operationStep) error {
	active := ""
	archiveRead := false
	bufferedLines := 0
	var registrationType uint64
	for index, step := range steps {
		code, _ := operationProtocolCode(step.resolved.Definition.ProtocolCode)
		if code == 0x80 {
			controlInterface, present := operationUintParam(step.Params, "controlInterface")
			if present && controlInterface == 1 && index != len(steps)-1 {
				return errors.New("switching the control interface to Ethernet must be the final command in an operation")
			}
		}
		if active == "archive" && code != 0x36 {
			if !archiveRead {
				return errors.New("archive selection 0x35 must be followed by at least one 0x36 read")
			}
			active = ""
		}
		// The printer buffer is independent of an open normal receipt. It may
		// be filled before opening the receipt or while adding its contents;
		// finalizing that receipt consumes the buffer (additionalText takes
		// precedence when supplied). No pending buffer escapes this operation.
		if code == 0x64 {
			if active != "" && active != "receipt" {
				return fmt.Errorf("commands[%d] appends receipt text while %s is unfinished", index, active)
			}
			bufferedLines += operationBufferLineCount(step.Params)
			if bufferedLines > 255 {
				return errors.New("operation exceeds the cumulative 255-line printer buffer limit")
			}
			continue
		}
		if code == 0x65 {
			if active != "" {
				return fmt.Errorf("commands[%d] prints the text buffer while %s is unfinished", index, active)
			}
			if bufferedLines == 0 {
				return errors.New("printing the text buffer requires preceding buffered lines in this operation")
			}
			bufferedLines = 0
			continue
		}
		start := ""
		switch code {
		case 0x12:
			start = "registration"
		case 0x21:
			start = "shift-open"
		case 0x29:
			start = "shift-close"
		case 0x23:
			start = "receipt"
		case 0x25:
			start = "correction"
		case 0x27:
			start = "settlement-report"
		case 0x35:
			start = "archive"
		}
		if start != "" {
			if active != "" {
				return fmt.Errorf("commands[%d] starts %s while %s is unfinished", index, start, active)
			}
			active, archiveRead = start, false
			if code == 0x12 {
				registrationType, _ = operationUintParam(step.Params, "reportType")
			}
			continue
		}
		if code == 0x10 && active != "archive" {
			active = ""
			continue
		}
		finish := ""
		switch code {
		case 0x13:
			finish = "registration"
		case 0x22:
			finish = "shift-open"
		case 0x2A:
			finish = "shift-close"
		case 0x24:
			finish = "receipt"
		case 0x26:
			finish = "correction"
		case 0x28:
			finish = "settlement-report"
		}
		if finish != "" {
			if active != finish {
				return fmt.Errorf("commands[%d] completes %s without its matching start", index, finish)
			}
			if code == 0x13 {
				_, hasReason := operationUintParam(step.Params, "reregistrationReason")
				if registrationType == 0 && hasReason {
					return errors.New("initial registration must omit reregistrationReason")
				}
				if registrationType != 0 && !hasReason {
					return errors.New("reregistration requires reregistrationReason")
				}
			}
			if code == 0x24 {
				bufferedLines = 0
			}
			active = ""
			continue
		}
		allowed := false
		switch code {
		case 0x16:
			allowed = active == "registration"
		case 0x2F:
			allowed = active == "shift-open" || active == "shift-close"
		case 0x19, 0x1F, 0x2B, 0x2C, 0x2D:
			allowed = active == "receipt"
		case 0x2E, 0x3F:
			allowed = active == "correction"
		case 0x36:
			allowed = active == "archive"
			archiveRead = true
		default:
			if active != "" && !step.resolved.Definition.ReadOnly {
				return fmt.Errorf("commands[%d] changes device state while %s is unfinished", index, active)
			}
			continue
		}
		if !allowed {
			return fmt.Errorf("commands[%d] (%s) has no matching active workflow", index, step.Command)
		}
	}
	if active == "archive" && archiveRead {
		active = ""
	}
	if active != "" {
		return fmt.Errorf("operation leaves %s unfinished", active)
	}
	if bufferedLines != 0 {
		return errors.New("operation leaves printer text buffered; finish with 0x65 or finalize a normal receipt with 0x24")
	}
	return nil
}

func operationBufferLineCount(raw json.RawMessage) int {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil {
		if value, present := object["lines"]; present {
			var lines []json.RawMessage
			if json.Unmarshal(value, &lines) == nil {
				return len(lines)
			}
		}
	}
	// The production 0x64 schema requires a nonempty lines array. Counting a
	// placeholder line here also lets narrow custom/test handlers use the same
	// operation scheduler without depending on that production parameter shape.
	return 1
}

func operationUintParam(raw json.RawMessage, name string) (uint64, bool) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object[name] == nil {
		return 0, false
	}
	value := strings.TrimSpace(string(object[name]))
	if strings.HasPrefix(value, `"`) {
		var text string
		if json.Unmarshal(object[name], &text) != nil {
			return 0, false
		}
		value = text
	}
	n, err := strconv.ParseUint(value, 10, 64)
	return n, err == nil
}

type operationObserver struct {
	client            *terminalfa.Client
	stepMutates       bool
	mutationSucceeded bool
	archiveActive     bool
	archiveExpected   int
	archiveReceived   int
	archiveReads      int
}

func (o *operationObserver) Exchange(ctx context.Context, code byte, payload []byte) ([]byte, error) {
	if code == 0x36 && o.archiveReads >= maxArchiveReads {
		return nil, fmt.Errorf("archive exceeds %d read commands", maxArchiveReads)
	}
	if code == 0x36 && o.archiveActive && o.archiveReceived >= o.archiveExpected {
		return nil, errors.New("archive document is already complete")
	}
	data, err := o.client.Call(ctx, code, payload)
	if err != nil {
		return nil, err
	}
	if o.stepMutates {
		o.mutationSucceeded = true
	}
	switch code {
	case 0x35:
		if len(data) != 4 {
			return nil, fmt.Errorf("archive selection response requires 4 bytes, received %d", len(data))
		}
		o.archiveActive = true
		o.archiveExpected = int(binary.LittleEndian.Uint16(data[2:]))
		o.archiveReceived, o.archiveReads = 0, 0
	case 0x36:
		if !o.archiveActive {
			return nil, errors.New("archive read has no active document selection")
		}
		o.archiveReads++
		if o.archiveReads > maxArchiveReads {
			return nil, fmt.Errorf("archive exceeds %d read commands", maxArchiveReads)
		}
		if len(data) == 0 {
			return nil, errors.New("archive read made no progress")
		}
		o.archiveReceived += len(data)
		if o.archiveReceived > o.archiveExpected || o.archiveReceived > maxArchiveBytes {
			return nil, errors.New("archive response exceeds the selected document length")
		}
	}
	return data, nil
}

func (o *operationObserver) completeArchive() error {
	if o.archiveActive && o.archiveReceived != o.archiveExpected {
		return fmt.Errorf("archive operation read %d of %d document bytes", o.archiveReceived, o.archiveExpected)
	}
	o.archiveActive = false
	return nil
}

func runOperation(ctx context.Context, device *Device, steps []operationStep) (any, error) {
	results := make([]operationResult, 0, len(steps))
	err := device.Client.WithSession(ctx, func(session *terminalfa.Client) error {
		observer := &operationObserver{client: session}
		scoped := *device
		scoped.Client = terminalfa.NewClient(observer)
		for index, step := range steps {
			code, _ := operationProtocolCode(step.resolved.Definition.ProtocolCode)
			if observer.archiveActive && code != 0x36 {
				if err := observer.completeArchive(); err != nil {
					return &UncertainOperationError{Step: index + 1, Command: step.Command, Cause: err}
				}
			}
			observer.stepMutates = !step.resolved.Definition.ReadOnly
			priorMutation := observer.mutationSucceeded
			var data any
			var err error
			if step.RepeatUntilComplete {
				chunks := make([]any, 0)
				before := observer.archiveReceived
				for observer.archiveActive && observer.archiveReceived < observer.archiveExpected {
					if err = ctx.Err(); err != nil {
						break
					}
					data, err = step.resolved.Execute(ctx, &scoped, step.Params)
					if err != nil {
						break
					}
					chunks = append(chunks, data)
				}
				data = map[string]any{"chunks": chunks, "byteCount": observer.archiveReceived - before}
			} else {
				data, err = step.resolved.Execute(ctx, &scoped, step.Params)
			}
			if err != nil {
				if priorMutation || observer.mutationSucceeded {
					return &UncertainOperationError{Step: index + 1, Command: step.Command, Cause: err}
				}
				return fmt.Errorf("operation step %d (%s): %w", index+1, step.Command, err)
			}
			results = append(results, operationResult{Command: step.Command, Data: data})
		}
		if err := observer.completeArchive(); err != nil {
			return &UncertainOperationError{Step: len(steps), Command: steps[len(steps)-1].Command, Cause: err}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"results": results}, nil
}

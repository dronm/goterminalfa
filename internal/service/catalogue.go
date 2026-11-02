package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/dronm/goterminalfa/internal/protocol"
)

// ParameterDefinition describes the public JSON type and its independent wire
// representation. Byte lengths apply after CP866 encoding, never to UTF-8 bytes.
type ParameterDefinition struct {
	Name          string                `json:"name"`
	TitleRU       string                `json:"titleRu"`
	Description   string                `json:"description"`
	JSONType      string                `json:"jsonType"`
	WireType      string                `json:"wireType"`
	Encoding      string                `json:"encoding,omitempty"`
	FixedLength   int                   `json:"fixedLength,omitempty"`
	MinLength     int                   `json:"minLength,omitempty"`
	MaxLength     int                   `json:"maxLength,omitempty"`
	Required      bool                  `json:"required"`
	Tag           uint16                `json:"tag,omitempty"`
	Repeated      bool                  `json:"repeated,omitempty"`
	MinItems      int                   `json:"minItems,omitempty"`
	MaxItems      int                   `json:"maxItems,omitempty"`
	Children      []ParameterDefinition `json:"children,omitempty"`
	Enum          map[string]string     `json:"enum,omitempty"`
	Bits          map[string]string     `json:"bits,omitempty"`
	Min           *uint64               `json:"min,omitempty"`
	Max           *uint64               `json:"max,omitempty"`
	Unit          string                `json:"unit,omitempty"`
	Notes         string                `json:"notes,omitempty"`
	RequiredIf    string                `json:"requiredIf,omitempty"`
	Padding       string                `json:"padding,omitempty"`
	AllowZeroDate bool                  `json:"allowZeroDate,omitempty"`
}

type Source struct {
	Document string `json:"document"`
	Version  string `json:"version"`
	Page     int    `json:"page"`
}

type CommandDefinition struct {
	Name                string                `json:"name"`
	ProtocolProfile     string                `json:"protocolProfile,omitempty"`
	Aliases             []string              `json:"aliases"`
	ProtocolCode        string                `json:"protocolCode"`
	Category            string                `json:"category"`
	TitleRU             string                `json:"titleRu"`
	Description         string                `json:"description"`
	DescriptionRU       string                `json:"descriptionRu"`
	ReadOnly            bool                  `json:"readOnly"`
	IdempotencyRequired bool                  `json:"idempotencyRequired"`
	Implemented         bool                  `json:"implemented"`
	UnavailableReason   string                `json:"unavailableReason,omitempty"`
	OperationRequired   bool                  `json:"operationRequired,omitempty"`
	RequestLayout       string                `json:"requestLayout"`
	ResponseLayout      string                `json:"responseLayout"`
	Input               []ParameterDefinition `json:"input"`
	Output              []ParameterDefinition `json:"output"`
	Notes               []string              `json:"notes,omitempty"`
	Preconditions       []string              `json:"preconditions,omitempty"`
	Source              Source                `json:"source"`
}

// Command binds metadata to explicit validation and execution. No arbitrary
// protocol code or unvalidated raw payload can be submitted through HTTP.
type Command struct {
	Definition        CommandDefinition
	Canonicalize      func(json.RawMessage) (json.RawMessage, error)
	Validate          func(json.RawMessage) error
	ValidateForDevice func(*Device, json.RawMessage) error
	Execute           func(context.Context, *Device, json.RawMessage) (any, error)
	// OnError runs after the failed primary session has closed, while the
	// service still owns the device gate and durable operation claim. The hook
	// receives the original device so recovery can open a fresh session.
	OnError func(context.Context, *Device, any, error) (any, error)
}

type SerialNumberResult struct {
	SerialNumber string `json:"serialNumber"`
}

type commandSpec struct {
	Code                                                byte
	Name, TitleRU, Category, Description, DescriptionRU string
	Aliases                                             []string
	Page                                                int
	ReadOnly                                            bool
	RequestLayout, ResponseLayout                       string
	Input, Output                                       []ParameterDefinition
	Notes, Preconditions                                []string
	Encode                                              func(json.RawMessage) ([]byte, error)
	Decode                                              func([]byte) (any, error)
	Validate                                            func(map[string]json.RawMessage) error
}

func limit(n uint64) *uint64 { return &n }

func makeCommand(spec commandSpec) Command {
	if spec.RequestLayout == "" {
		spec.RequestLayout = "positional"
	}
	if spec.ResponseLayout == "" {
		spec.ResponseLayout = "positional"
	}
	if spec.Input == nil {
		spec.Input = []ParameterDefinition{}
	}
	if spec.Output == nil {
		spec.Output = []ParameterDefinition{}
	}
	aliases := append([]string{}, spec.Aliases...)
	for _, alias := range []string{fmt.Sprintf("0x%02X", spec.Code), fmt.Sprintf("0x%02x", spec.Code)} {
		found := false
		for _, old := range aliases {
			if old == alias {
				found = true
			}
		}
		if !found {
			aliases = append(aliases, alias)
		}
	}
	encode := func(params json.RawMessage) ([]byte, error) {
		payload, err := encodeFields(spec.Input, spec.RequestLayout, params)
		if err != nil {
			return nil, err
		}
		if spec.Validate != nil {
			object, err := decodeParams(params)
			if err != nil {
				return nil, err
			}
			if err := spec.Validate(object); err != nil {
				return nil, err
			}
		}
		if spec.Encode != nil {
			payload, err = spec.Encode(params)
		}
		if err != nil {
			return nil, err
		}
		if len(payload) > 1023 {
			return nil, fmt.Errorf("encoded command data exceeds 1023 bytes")
		}
		return payload, nil
	}
	return Command{
		Definition: CommandDefinition{
			Name: spec.Name, Aliases: aliases, ProtocolCode: fmt.Sprintf("0x%02X", spec.Code),
			Category: spec.Category, TitleRU: spec.TitleRU, Description: spec.Description, DescriptionRU: spec.DescriptionRU,
			ReadOnly: spec.ReadOnly, IdempotencyRequired: !spec.ReadOnly, Implemented: true,
			Input: spec.Input, Output: spec.Output, Notes: spec.Notes, Preconditions: spec.Preconditions,
			RequestLayout: spec.RequestLayout, ResponseLayout: spec.ResponseLayout,
			Source: Source{Document: "Протокол информационного обмена ККТ Терминал-ФА / ККТ КИТ Онлайн-Ф", Version: "01.05", Page: spec.Page},
		},
		Validate: func(params json.RawMessage) error { _, err := encode(params); return err },
		Execute: func(ctx context.Context, device *Device, params json.RawMessage) (any, error) {
			payload, err := encode(params)
			if err != nil {
				return nil, err
			}
			data, err := device.Client.Call(ctx, spec.Code, payload)
			if err != nil {
				return nil, err
			}
			var result any
			if spec.Decode != nil {
				result, err = spec.Decode(data)
			} else {
				result, err = decodeFields(spec.Output, spec.ResponseLayout, data)
			}
			if err != nil {
				return nil, fmt.Errorf("%w: %s response: %w", protocol.ErrMalformedFrame, spec.Name, err)
			}
			if (spec.Code == 0x01 || spec.Code == 0x02) && device.ExpectedSerialNumber != "" && len(data) >= 12 && string(data[:12]) != device.ExpectedSerialNumber {
				return nil, &IdentityError{Expected: device.ExpectedSerialNumber, Actual: string(data[:12])}
			}
			return result, nil
		},
	}
}

func unavailableCommand(code byte, name, title, reason string) Command {
	page := 7
	if code == 0x34 {
		page = 8
	}
	cmd := makeCommand(commandSpec{Code: code, Name: name, TitleRU: title, Category: "unresolved", Page: page, ReadOnly: code == 0x34,
		Description: reason, DescriptionRU: "Команда перечислена в протоколе, но формат входных и выходных данных не определен."})
	cmd.Definition.Implemented = false
	cmd.Definition.UnavailableReason = reason
	return cmd
}

func BuiltinCommands() []Command {
	return builtinCommands(time.Now)
}

func builtinCommands(now func() time.Time) []Command {
	var commands []Command
	commands = append(commands, informationCommands()...)
	commands = append(commands, registrationCommands()...)
	commands = append(commands, receiptCommands()...)
	commands = append(commands, archivePrintCommands()...)
	commands = append(commands, statusCounterCommands()...)
	commands = append(commands, settingsCommands()...)
	commands = append(commands,
		unavailableCommand(0x14, "beginFiscalStorageClosure", "Начать отчет о закрытии ФН", "Listed in the base protocol without a payload definition. The separate FFD 1.2 supplement is not silently substituted for the configured base protocol."),
		unavailableCommand(0x15, "finalizeFiscalStorageClosure", "Сформировать отчет о закрытии ФН", "The base protocol provides no payload definition; the FFD 1.2 supplement uses a conflicting command code. Manufacturer clarification is required."),
		unavailableCommand(0x17, "sendFiscalStorageClosureData", "Передать данные отчета о закрытии ФН", "Listed without a payload definition in the base protocol. FFD 1.2-specific fields are not assumed to apply to this base protocol."),
		unavailableCommand(0x34, "getActivationParameter", "Запрос параметра активации ФН по номеру тэга", "The command is listed, but its request and response layouts are not documented."),
	)
	sort.Slice(commands, func(i, j int) bool { return commands[i].Definition.ProtocolCode < commands[j].Definition.ProtocolCode })
	for i := range commands {
		commands[i].Definition.OperationRequired = commands[i].Definition.Implemented && isWorkflowCommand(commands[i].Definition.ProtocolCode)
	}
	for _, command := range commands {
		if command.Definition.Name == "setDateTime" {
			commands = append(commands, syncDateTimeCommand(command, now))
			break
		}
	}
	commands = append(commands, operationCommand(commands))
	return commands
}

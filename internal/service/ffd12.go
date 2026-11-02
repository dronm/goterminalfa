package service

import (
	"fmt"
	"sort"
)

// BuiltinCommandsForProfile selects explicit wire definitions. The base
// catalogue remains the default; the supplement does not redefine every command.
func BuiltinCommandsForProfile(profile string) ([]Command, error) {
	if profile == "" || profile == "base" {
		return BuiltinCommands(), nil
	}
	if profile != "ffd1.2" {
		return nil, fmt.Errorf("unsupported protocol profile %q", profile)
	}
	base := BuiltinCommands()
	byCode := make(map[string]Command)
	var applications []Command
	for _, cmd := range base {
		if cmd.Definition.ProtocolCode == "" {
			if cmd.Definition.Name != "executeOperation" {
				applications = append(applications, cmd)
			}
			continue
		}
		byCode[cmd.Definition.ProtocolCode] = cmd
	}
	overrides := append(ffd12ShiftCommands(base), ffd12ReceiptCommands()...)
	overrides = append(overrides, ffd12ExtraCommands(base)...)
	for _, cmd := range overrides {
		byCode[cmd.Definition.ProtocolCode] = cmd
	}
	// The supplement supplies different commands for these fiscal operations.
	// Keep their legacy wire schemas out of an explicitly FFD 1.2 device profile.
	for code, replacement := range map[string]string{
		"0x12": "0xE1/0xE2/0xE3 registration commands (not implemented in this release)",
		"0x13": "0xE1/0xE2/0xE3 registration commands (not implemented in this release)",
		"0x16": "0xE1/0xE2/0xE3 registration commands (not implemented in this release)",
		"0x1F": "setReceiptAdditionalData (0x98)",
		"0x2B": "addReceiptItemUnmarked (0x96) for unmarked goods",
		"0x2C": "item-specific agent fields in addReceiptItemUnmarked (0x96)",
		"0x2E": "setReceiptAdditionalData (0x98), item data (0x96), and payment data (0x2D)",
		"0x3F": "setReceiptAdditionalData (0x98)",
	} {
		cmd := byCode[code]
		cmd.Definition.Implemented = false
		cmd.Definition.UnavailableReason = "This base-protocol command is superseded in the FFD 1.2 profile; use " + replacement + ". Select the base profile only when intentionally targeting the base protocol."
		byCode[code] = cmd
	}
	// The supplement defines 0x14/0x17, but labels closure finalization 0x28,
	// which is also its settlement-report finalizer with a different response.
	// Do not expose a partial or guessed fiscal-storage closure workflow.
	for _, code := range []string{"0x14", "0x15", "0x17"} {
		cmd := byCode[code]
		reason := "The FFD 1.2 supplement defines closure-start/data payloads but assigns finalization to 0x28, also used for settlement reports with a different response. The complete closure workflow remains unavailable pending manufacturer clarification."
		cmd.Definition.UnavailableReason = reason
		cmd.Definition.Description = reason
		cmd.Definition.DescriptionRU = "Форматы начала и данных закрытия ФН описаны, но код завершающей команды 0x28 конфликтует с отчетом о состоянии расчетов. Полная процедура закрытия ФН не включена до уточнения изготовителем."
		cmd.Definition.Source = Source{Document: "Протокол информационного обмена ККТ Терминал-ФА: дополнение для работы с ФФД версии 1.2", Version: "FFD 1.2", Page: 13}
		byCode[code] = cmd
	}
	commands := make([]Command, 0, len(byCode)+len(applications)+1)
	for _, cmd := range byCode {
		cmd.Definition.ProtocolProfile = profile
		cmd.Definition.OperationRequired = cmd.Definition.Implemented && isWorkflowCommand(cmd.Definition.ProtocolCode)
		commands = append(commands, cmd)
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Definition.ProtocolCode < commands[j].Definition.ProtocolCode })
	for _, cmd := range applications {
		cmd.Definition.ProtocolProfile = profile
		commands = append(commands, cmd)
	}
	commands = append(commands, createReceiptCommand(commands))
	operation := operationCommand(commands)
	operation.Definition.ProtocolProfile = profile
	commands = append(commands, operation)
	return commands, nil
}

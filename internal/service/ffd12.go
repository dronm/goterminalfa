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
	var contextualCommands []Command
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
	overrides = append(overrides, ffd12ActivationCommand())
	overrides = append(overrides, ffd12HostCommands()...)
	overrides = append(overrides, ffd12DiagnosticCommands()...)
	for _, command := range ffd12FiscalStorageClosureCommands() {
		if command.Definition.ProtocolCode == "0x28" {
			// Keep both response schemas. The numeric alias is bound to the
			// preceding report start while preparing a complete operation.
			contextualCommands = append(contextualCommands, command)
		} else {
			overrides = append(overrides, command)
		}
	}
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
	// The legacy byte remains unavailable; the canonical closure name now
	// refers to the context-specific 0x28 finalizer from the supplement.
	legacyClosure := byCode["0x15"]
	legacyClosure.Definition.Name = "finalizeFiscalStorageClosureLegacy"
	legacyClosure.Definition.UnavailableReason = "FFD 1.2 closes the fiscal-storage report with 0x28 after 0x14 in a complete executeOperation. Use finalizeFiscalStorageClosure; legacy command 0x15 is unavailable."
	legacyClosure.Definition.Description = legacyClosure.Definition.UnavailableReason
	legacyClosure.Definition.DescriptionRU = "Для завершения отчета о закрытии ФН в ФФД 1.2 используется команда 0x28 после 0x14 в составе одной операции. Устаревшая команда 0x15 не поддерживается."
	legacyClosure.Definition.Source = ffd12ExtraSource(13)
	byCode["0x15"] = legacyClosure
	commands := make([]Command, 0, len(byCode)+len(contextualCommands)+len(applications)+1)
	for _, cmd := range byCode {
		commands = append(commands, cmd)
	}
	commands = append(commands, contextualCommands...)
	for i := range commands {
		commands[i].Definition.ProtocolProfile = profile
		commands[i].Definition.OperationRequired = commands[i].Definition.Implemented && isWorkflowCommand(commands[i].Definition.ProtocolCode)
	}
	sort.Slice(commands, func(i, j int) bool {
		if commands[i].Definition.ProtocolCode == commands[j].Definition.ProtocolCode {
			return commands[i].Definition.Name < commands[j].Definition.Name
		}
		return commands[i].Definition.ProtocolCode < commands[j].Definition.ProtocolCode
	})
	for _, cmd := range applications {
		cmd.Definition.ProtocolProfile = profile
		commands = append(commands, cmd)
	}
	commands = append(commands, createReceiptCommand(commands))
	commands = append(commands, createCorrectionReceiptCommand(commands))
	operation := operationCommand(commands)
	operation.Definition.ProtocolProfile = profile
	commands = append(commands, operation)
	return commands, nil
}

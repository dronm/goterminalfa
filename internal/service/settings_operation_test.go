package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEthernetControlSwitchMustFinishBatch(t *testing.T) {
	var operation Command
	for _, command := range BuiltinCommands() {
		if command.Definition.Name == "executeOperation" {
			operation = command
		}
	}
	for _, tc := range []struct {
		name, params string
		valid        bool
	}{
		{"Ethernet last", `{"commands":[{"cmd":"getControlSettings"},{"cmd":"setControlSettings","params":{"controlInterface":1,"tcpPort":7778}}]}`, true},
		{"Ethernet followed by query", `{"commands":[{"cmd":"setControlSettings","params":{"controlInterface":1,"tcpPort":7778}},{"cmd":"getControlSettings"}]}`, false},
		{"Serial followed by query", `{"commands":[{"cmd":"setControlSettings","params":{"controlInterface":0,"tcpPort":7778}},{"cmd":"getControlSettings"}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := operation.Validate(json.RawMessage(tc.params))
			if tc.valid && err != nil {
				t.Fatal(err)
			}
			if !tc.valid && (err == nil || !strings.Contains(err.Error(), "final command")) {
				t.Fatalf("expected terminal-switch error, got %v", err)
			}
		})
	}
}

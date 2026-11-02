// Package service schedules complete operations per device and applies each
// command's idempotency policy independently of HTTP and the wire transport.
package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	terminalfa "github.com/dronm/goterminalfa"
	"github.com/dronm/goterminalfa/internal/config"
	"github.com/dronm/goterminalfa/internal/protocol"
	"github.com/dronm/goterminalfa/internal/store"
	"github.com/sirupsen/logrus"
)

type Request struct {
	DeviceID  string          `json:"deviceId"`
	Command   string          `json:"cmd"`
	Params    json.RawMessage `json:"params,omitempty"`
	RequestID string          `json:"requestId,omitempty"`
}

type Error struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	DeviceCode *byte  `json:"deviceCode,omitempty"`
}

type Response struct {
	DeviceID  string `json:"deviceId,omitempty"`
	Command   string `json:"cmd,omitempty"`
	RequestID string `json:"requestId,omitempty"`
	Status    string `json:"status"`
	Data      any    `json:"data,omitempty"`
	Error     *Error `json:"error,omitempty"`
}

type Reply struct {
	HTTPStatus int
	Body       []byte
	Replayed   bool
}

func JSONReply(status int, response Response) Reply {
	body, err := json.Marshal(response)
	if err != nil {
		return Reply{HTTPStatus: 500, Body: []byte(`{"status":"unknown","error":{"code":"internal_error","message":"encode response failed"}}`)}
	}
	return Reply{HTTPStatus: status, Body: body}
}

func Failure(req Request, status int, code, message string) Reply {
	return JSONReply(status, Response{DeviceID: req.DeviceID, Command: req.Command, RequestID: req.RequestID,
		Status: "failed", Error: &Error{Code: code, Message: message}})
}

type Device struct {
	ID                   string
	ProtocolProfile      string
	ConnectionType       string
	ExpectedSerialNumber string
	Client               *terminalfa.Client
	gate                 chan struct{}
	slots                chan struct{}
}

func NewDevice(id, connectionType, expectedSerial string, client *terminalfa.Client, queueSize int) *Device {
	if queueSize < 1 {
		queueSize = 32
	}
	return &Device{ID: id, ProtocolProfile: "base", ConnectionType: connectionType, ExpectedSerialNumber: expectedSerial, Client: client,
		gate: make(chan struct{}, 1), slots: make(chan struct{}, queueSize+1)}
}

var errQueueFull = errors.New("device waiting queue is full")

func (d *Device) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case d.slots <- struct{}{}:
	default:
		return nil, errQueueFull
	}
	select {
	case d.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-d.gate
			<-d.slots
			return nil, err
		}
		return func() { <-d.gate; <-d.slots }, nil
	case <-ctx.Done():
		<-d.slots
		return nil, ctx.Err()
	}
}

type DeviceInfo struct {
	ID                   string `json:"id"`
	ProtocolProfile      string `json:"protocolProfile"`
	ConnectionType       string `json:"connectionType"`
	ExpectedSerialNumber string `json:"expectedSerialNumber,omitempty"`
}

type Service struct {
	devices  map[string]*Device
	commands map[string]Command
	list     []CommandDefinition
	profiles map[string]*commandCatalogue
	journal  *store.Store
	logger   *logrus.Logger
}

type commandCatalogue struct {
	commands map[string]Command
	list     []CommandDefinition
}

func New(devices []*Device, commands []Command, journal *store.Store, logger *logrus.Logger) (*Service, error) {
	if journal == nil || logger == nil {
		return nil, errors.New("journal and logger are required")
	}
	s := &Service{devices: map[string]*Device{}, commands: map[string]Command{}, profiles: map[string]*commandCatalogue{}, journal: journal, logger: logger}
	for _, d := range devices {
		if d == nil || !config.ValidID.MatchString(d.ID) || d.Client == nil || s.devices[d.ID] != nil {
			return nil, errors.New("invalid or duplicate device")
		}
		s.devices[d.ID] = d
		if d.ProtocolProfile == "" {
			d.ProtocolProfile = "base"
		}
		if d.ProtocolProfile != "base" && d.ProtocolProfile != "ffd1.2" {
			return nil, fmt.Errorf("unsupported protocol profile %q for %s", d.ProtocolProfile, d.ID)
		}
	}
	for _, cmd := range commands {
		if cmd.Definition.Name == "" || cmd.Execute == nil || cmd.Validate == nil {
			return nil, errors.New("command name, validator and executor are required")
		}
		// Every mutating command must use durable idempotency. Read-only commands
		// deliberately bypass SQLite, even if a tracing requestId was supplied.
		if cmd.Definition.ReadOnly == cmd.Definition.IdempotencyRequired {
			return nil, fmt.Errorf("command %s has an inconsistent idempotency policy", cmd.Definition.Name)
		}
		for _, name := range append([]string{cmd.Definition.Name}, cmd.Definition.Aliases...) {
			if name == "" {
				return nil, errors.New("empty command alias")
			}
			if _, ok := s.commands[name]; ok {
				return nil, fmt.Errorf("duplicate command name %q", name)
			}
			s.commands[name] = cmd
		}
		s.list = append(s.list, cmd.Definition)
	}
	s.profiles["base"] = &commandCatalogue{commands: s.commands, list: s.list}
	for _, d := range devices {
		if d.ProtocolProfile == "base" || s.profiles[d.ProtocolProfile] != nil {
			continue
		}
		commands, err := BuiltinCommandsForProfile(d.ProtocolProfile)
		if err != nil {
			return nil, err
		}
		catalogue := &commandCatalogue{commands: make(map[string]Command)}
		for _, cmd := range commands {
			for _, name := range append([]string{cmd.Definition.Name}, cmd.Definition.Aliases...) {
				if _, found := catalogue.commands[name]; found {
					return nil, fmt.Errorf("duplicate %s command name %q", d.ProtocolProfile, name)
				}
				catalogue.commands[name] = cmd
			}
			catalogue.list = append(catalogue.list, cmd.Definition)
		}
		s.profiles[d.ProtocolProfile] = catalogue
	}
	return s, nil
}

func (s *Service) Commands() []CommandDefinition { return s.list }

func (s *Service) CommandsForDevice(deviceID string) ([]CommandDefinition, string, error) {
	if !config.ValidID.MatchString(deviceID) {
		return nil, "", errors.New("invalid deviceId")
	}
	d, found := s.devices[deviceID]
	if !found {
		return nil, "", errors.New("unknown device")
	}
	return s.profiles[d.ProtocolProfile].list, d.ProtocolProfile, nil
}

func (s *Service) Healthy(ctx context.Context) error { return s.journal.Ping(ctx) }

func (s *Service) Devices() []DeviceInfo {
	list := make([]DeviceInfo, 0, len(s.devices))
	for _, d := range s.devices {
		list = append(list, DeviceInfo{ID: d.ID, ProtocolProfile: d.ProtocolProfile, ConnectionType: d.ConnectionType, ExpectedSerialNumber: d.ExpectedSerialNumber})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list
}

func canonicalParams(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, errors.New("params must be a JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("params must contain exactly one JSON object")
	}
	return json.Marshal(object)
}

func fingerprint(command string, params json.RawMessage) string {
	sum := sha256.Sum256(append([]byte(command+"\x00"), params...))
	return hex.EncodeToString(sum[:])
}

func (s *Service) Execute(ctx context.Context, req Request) Reply {
	if !config.ValidID.MatchString(req.DeviceID) || (req.RequestID != "" && !config.ValidID.MatchString(req.RequestID)) {
		return Failure(req, 400, "invalid_request", "deviceId and requestId must contain 1–128 ASCII letters, digits, dots, underscores or hyphens, starting with a letter or digit")
	}
	device, ok := s.devices[req.DeviceID]
	if !ok {
		return Failure(req, 404, "unknown_device", "device is not configured")
	}
	cmd, ok := s.profiles[device.ProtocolProfile].commands[req.Command]
	if !ok {
		return Failure(req, 404, "unknown_command", "command is not implemented")
	}
	req.Command = cmd.Definition.Name
	if cmd.Definition.UnavailableReason != "" {
		return Failure(req, 501, "command_unavailable", cmd.Definition.UnavailableReason)
	}
	params, err := canonicalParams(req.Params)
	if err != nil {
		return Failure(req, 400, "invalid_request", err.Error())
	}
	if cmd.Canonicalize != nil {
		params, err = cmd.Canonicalize(params)
		if err != nil {
			return Failure(req, 400, "invalid_request", err.Error())
		}
	}
	req.Params = params
	if err := cmd.Validate(params); err != nil {
		return Failure(req, 400, "invalid_request", err.Error())
	}
	if cmd.ValidateForDevice != nil {
		if err := cmd.ValidateForDevice(device, params); err != nil {
			return Failure(req, 400, "invalid_request", err.Error())
		}
	}
	if isWorkflowCommand(cmd.Definition.ProtocolCode) {
		return Failure(req, 400, "operation_required", "this stateful command must be part of a complete executeOperation request")
	}
	key := fingerprint(req.Command, params)
	if device.ProtocolProfile != "base" {
		// Keep legacy fingerprints stable. A profile switch cannot reuse a saved
		// request under a different wire format; the old claim causes conflict.
		key = fingerprint(device.ProtocolProfile+":"+req.Command, params)
	}
	if cmd.Definition.IdempotencyRequired {
		if req.RequestID == "" {
			return Failure(req, 400, "invalid_request", "requestId is required for this command")
		}
		record, err := s.journal.Get(ctx, req.DeviceID, req.RequestID)
		if err == nil {
			if record.Fingerprint != key {
				return Failure(req, 409, "idempotency_conflict", store.ErrConflict.Error())
			}
			return recordedReply(req, record)
		}
		if !errors.Is(err, store.ErrNotFound) {
			return s.storageFailure(req, err)
		}
	}

	release, err := device.acquire(ctx)
	if err != nil {
		if errors.Is(err, errQueueFull) {
			return Failure(req, 503, "queue_full", err.Error())
		}
		return Failure(req, 504, "device_timeout", "request canceled or timed out while waiting for the device; no command was sent")
	}
	defer release()

	if cmd.Definition.IdempotencyRequired {
		claim, err := s.journal.ClaimRequest(ctx, req.DeviceID, req.RequestID, key, req.Command, params)
		if errors.Is(err, store.ErrConflict) {
			return Failure(req, 409, "idempotency_conflict", err.Error())
		}
		if err != nil {
			return s.storageFailure(req, err)
		}
		if !claim.Acquired {
			return recordedReply(req, claim.Record)
		}
	}

	data, execErr := executeSafely(ctx, cmd, device, params)
	response := JSONReply(200, Response{DeviceID: req.DeviceID, Command: req.Command,
		RequestID: req.RequestID, Status: "completed", Data: data})
	if execErr != nil {
		response = executionFailure(req, execErr)
		s.logger.WithFields(logrus.Fields{"deviceId": req.DeviceID, "cmd": req.Command, "requestId": req.RequestID}).WithError(execErr).Warn("device operation failed")
	}

	if cmd.Definition.IdempotencyRequired {
		// Persistence must outlive a disconnected HTTP client. Once execution has
		// started, every uncertain outcome remains claimed and is never retried.
		persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var rejection *protocol.DeviceError
		var notExecuted *NotExecutedError
		var notSent *terminalfa.CommandNotSentError
		unknown := execErr != nil && !errors.As(execErr, &rejection) && !errors.As(execErr, &notExecuted) && !errors.As(execErr, &notSent)
		if unknown || response.HTTPStatus == 500 {
			if err := s.journal.MarkUnknown(persistCtx, req.DeviceID, req.RequestID); err != nil {
				s.logger.WithError(err).Error("persist unknown outcome failed; claim retained")
			}
			return unknownReply(req)
		}
		if err := s.journal.Complete(persistCtx, req.DeviceID, req.RequestID, response.HTTPStatus, response.Body); err != nil {
			s.logger.WithError(err).Error("persist result failed; claim retained")
			return unknownReply(req)
		}
	}
	return response
}

func executeSafely(ctx context.Context, cmd Command, d *Device, params json.RawMessage) (data any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("command handler panic: %v", recovered)
		}
	}()
	if cmd.Definition.ReadOnly {
		return cmd.Execute(ctx, d, params)
	}
	started := false
	err = d.Client.WithSession(ctx, func(client *terminalfa.Client) error {
		scoped := *d
		scoped.Client = client
		// Verify identity while owning the operation. Serial retains this physical
		// connection; the documented TCP protocol uses a fresh connection per
		// command to the same configured device endpoint.
		if d.ExpectedSerialNumber != "" {
			serial, err := client.SerialNumber(ctx)
			if err != nil {
				return err
			}
			if serial != d.ExpectedSerialNumber {
				return &IdentityError{Expected: d.ExpectedSerialNumber, Actual: serial}
			}
		}
		started = true
		var executeErr error
		data, executeErr = cmd.Execute(ctx, &scoped, params)
		return executeErr
	})
	if err != nil && !started {
		return nil, &NotExecutedError{Cause: err}
	}
	return data, err
}

// NotExecutedError identifies failures before any mutating command is sent,
// including opening, identity verification, and local clock validation. A later
// batch failure still becomes uncertain if an earlier step changed device state.
type NotExecutedError struct{ Cause error }

func (e *NotExecutedError) Error() string { return e.Cause.Error() }
func (e *NotExecutedError) Unwrap() error { return e.Cause }

type IdentityError struct{ Expected, Actual string }

func (e *IdentityError) Error() string {
	return fmt.Sprintf("expected KKT serial %s, received %s", e.Expected, e.Actual)
}

func executionFailure(req Request, err error) Reply {
	var rejection *protocol.DeviceError
	var identity *IdentityError
	var serial *terminalfa.InvalidSerialError
	switch {
	case errors.As(err, &rejection):
		return JSONReply(502, Response{DeviceID: req.DeviceID, Command: req.Command, RequestID: req.RequestID,
			Status: "failed", Error: &Error{Code: "device_error", Message: err.Error(), DeviceCode: &rejection.Code}})
	case errors.As(err, &identity):
		return Failure(req, 502, "device_identity_mismatch", err.Error())
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return Failure(req, 504, "device_timeout", "device operation timed out or was canceled")
	case errors.Is(err, protocol.ErrMalformedFrame), errors.Is(err, protocol.ErrChecksumMismatch), errors.As(err, &serial):
		return Failure(req, 502, "protocol_error", err.Error())
	default:
		return Failure(req, 502, "transport_error", "device communication failed; see server log")
	}
}

func unknownReply(req Request) Reply {
	return JSONReply(409, Response{DeviceID: req.DeviceID, Command: req.Command, RequestID: req.RequestID,
		Status: "unknown", Error: &Error{Code: "outcome_unknown", Message: "operation may have executed; reconcile with the device before taking further action; automatic retry is blocked"}})
}

func recordedReply(req Request, record store.Record) Reply {
	switch record.State {
	case store.StateCompleted:
		return Reply{HTTPStatus: record.HTTPStatus, Body: record.Response, Replayed: true}
	case store.StateInProgress:
		return JSONReply(409, Response{DeviceID: req.DeviceID, Command: req.Command, RequestID: req.RequestID,
			Status: "in_progress", Error: &Error{Code: "request_in_progress", Message: "operation is already in progress"}})
	default:
		return unknownReply(req)
	}
}

func (s *Service) storageFailure(req Request, err error) Reply {
	s.logger.WithError(err).Error("request journal failed")
	return Failure(req, http.StatusServiceUnavailable, "storage_error", "request journal is unavailable; command was not started")
}

func (s *Service) Lookup(ctx context.Context, deviceID, requestID string) Reply {
	req := Request{DeviceID: deviceID, RequestID: requestID}
	if !config.ValidID.MatchString(deviceID) || !config.ValidID.MatchString(requestID) {
		return Failure(req, 400, "invalid_request", "valid deviceId and requestId query parameters are required")
	}
	record, err := s.journal.Get(ctx, deviceID, requestID)
	if errors.Is(err, store.ErrNotFound) {
		return Failure(req, 404, "not_found", "no persisted request exists; read-only commands are not journaled")
	}
	if err != nil {
		return s.storageFailure(req, err)
	}
	return recordedReply(req, record)
}

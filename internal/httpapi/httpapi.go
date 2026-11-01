// Package httpapi exposes the driver through a versioned JSON API.
package httpapi

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/dronm/goterminalfa/internal/config"
	"github.com/dronm/goterminalfa/internal/service"
	"github.com/sirupsen/logrus"
)

type API struct {
	service *service.Service
	options config.HTTP
	logger  *logrus.Logger
}

func New(s *service.Service, options config.HTTP, logger *logrus.Logger) *API {
	return &API{service: s, options: options, logger: logger}
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	status := 500
	defer func() {
		a.logger.WithFields(logrus.Fields{"method": r.Method, "path": r.URL.Path,
			"status": status, "durationMs": time.Since(started).Milliseconds()}).Info("HTTP request")
	}()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	send := func(reply service.Reply) {
		status = reply.HTTPStatus
		if reply.Replayed {
			w.Header().Set("Idempotency-Replayed", "true")
		}
		w.WriteHeader(status)
		if _, err := w.Write(reply.Body); err != nil {
			a.logger.WithError(err).Debug("write HTTP response failed")
		}
	}
	fail := func(code int, kind, message string) {
		send(service.Failure(service.Request{}, code, kind, message))
	}
	method := func(allowed string) bool {
		if r.Method != allowed {
			w.Header().Set("Allow", allowed)
			fail(405, "method_not_allowed", "use "+allowed)
			return false
		}
		return true
	}
	if r.URL.Path != "/healthz" && a.options.BearerToken != "" {
		expected := "Bearer " + a.options.BearerToken
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(expected)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			fail(401, "unauthorized", "valid bearer token required")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), a.options.RequestTimeout.Value())
	defer cancel()
	switch r.URL.Path {
	case "/healthz":
		if method(http.MethodGet) {
			// No device commands are sent by liveness checks.
			if err := a.service.Healthy(ctx); err != nil {
				fail(503, "storage_error", "request journal is unavailable")
				return
			}
			status = 200
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"status":"ok"}`)
		}
	case "/api/v1/devices":
		if method(http.MethodGet) {
			send(marshal(200, map[string]any{"devices": a.service.Devices()}))
		}
	case "/api/v1/commands":
		if r.Method == http.MethodGet {
			send(marshal(200, map[string]any{"commands": a.service.Commands()}))
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "GET, POST")
			fail(405, "method_not_allowed", "use GET or POST")
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			fail(415, "invalid_request", "Content-Type must be application/json")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, a.options.MaxBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				fail(413, "invalid_request", "request body exceeds configured limit")
			} else {
				fail(400, "invalid_request", "cannot read JSON request")
			}
			return
		}
		var req service.Request
		if err := decodeRequest(body, &req); err != nil {
			fail(400, "invalid_request", err.Error())
			return
		}
		send(a.service.Execute(ctx, req))
	case "/api/v1/requests":
		if method(http.MethodGet) {
			send(a.service.Lookup(ctx, r.URL.Query().Get("deviceId"), r.URL.Query().Get("requestId")))
		}
	default:
		fail(404, "not_found", "endpoint does not exist")
	}
}

func marshal(status int, value any) service.Reply {
	body, err := json.Marshal(value)
	if err != nil {
		return service.Failure(service.Request{}, 500, "internal_error", "cannot encode response")
	}
	return service.Reply{HTTPStatus: status, Body: body}
}

// decodeRequest rejects duplicate keys as well as unknown envelope fields. This
// avoids ambiguous requests and future fingerprint/handler interpretation drift.
func decodeRequest(body []byte, dst *service.Request) error {
	if err := validateJSON(body); err != nil {
		return err
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil || envelope == nil {
		return errors.New("request must be a JSON object")
	}
	for name := range envelope {
		switch name {
		case "deviceId", "cmd", "params", "requestId":
		default:
			return fmt.Errorf("unknown request field %q; field names are case-sensitive", name)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON request: %w", err)
	}
	if dst.DeviceID == "" || dst.Command == "" {
		return errors.New("deviceId and cmd are required")
	}
	return nil
}

func validateJSON(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := jsonValue(decoder, 0); err != nil {
		return fmt.Errorf("invalid JSON request: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("request must contain one JSON object")
	}
	return nil
}

func jsonValue(decoder *json.Decoder, depth int) error {
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
				return errors.New("duplicate or invalid JSON object key")
			}
			keys[key] = true
			if err := jsonValue(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := jsonValue(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected closing delimiter")
	}
	_, err = decoder.Token()
	return err
}

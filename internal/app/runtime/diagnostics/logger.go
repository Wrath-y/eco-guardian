// Package diagnostics provides the single bounded operational event pipeline.
package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	aiaudit "github.com/zouyi/eco-guardian/internal/ai/audit"
)

const (
	Filename         = "eco-guardian.log"
	DisplayDirectory = "<project-directory>/logs"
)

var ErrLoggerInvalid = errors.New("runtime diagnostics logger is invalid")

type EventName string

const (
	EventStartupPhase         EventName = "startup_phase"
	EventListenerSelected     EventName = "listener_selected"
	EventPackageVerification  EventName = "package_verification"
	EventProcessLifecycle     EventName = "process_lifecycle"
	EventHealthTransition     EventName = "health_transition"
	EventCapabilityTransition EventName = "capability_transition"
	EventJobRecovery          EventName = "job_recovery"
	EventTaskReconciliation   EventName = "task_reconciliation"
	EventSafeError            EventName = "safe_error"
	EventChildOutput          EventName = "child_output"
	EventHTTPRequest          EventName = "http_request"
	EventConfigLoaded         EventName = "config_loaded"
)

type Correlation struct {
	RootRequestID     string `json:"root_request_id,omitempty"`
	AttemptRequestID  string `json:"attempt_request_id,omitempty"`
	JobID             string `json:"job_id,omitempty"`
	TaskID            string `json:"task_id,omitempty"`
	ProviderRequestID string `json:"provider_request_id,omitempty"`
	LaunchGeneration  uint64 `json:"launch_generation,omitempty"`
}

type Event struct {
	Name        EventName      `json:"event_name"`
	Component   string         `json:"component"`
	Phase       string         `json:"phase,omitempty"`
	State       string         `json:"state,omitempty"`
	Code        string         `json:"code,omitempty"`
	DurationMS  int64          `json:"duration_ms,omitempty"`
	Correlation Correlation    `json:"correlation,omitempty"`
	Fields      map[string]any `json:"fields,omitempty"`
}

type record struct {
	Timestamp time.Time `json:"timestamp"`
	Event
}

type Options struct {
	Directory string
	MaxBytes  int64
	MaxFiles  int
	Capacity  int
	Now       func() time.Time
	Secrets   [][]byte
}

type Logger struct {
	queue    chan []byte
	stop     chan struct{}
	done     chan error
	writer   *rotatingWriter
	now      func() time.Time
	redactor Redactor
	closed   atomic.Bool
	dropped  atomic.Uint64
	failures atomic.Uint64
}

func New(options Options) (*Logger, error) {
	if options.Directory == "" || options.MaxBytes < 64<<10 || options.MaxBytes > 1<<30 || options.MaxFiles < 1 || options.MaxFiles > 20 || options.Capacity < 1 || options.Capacity > 16384 {
		return nil, ErrLoggerInvalid
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	writer, err := newRotatingWriter(options.Directory, options.MaxBytes, options.MaxFiles)
	if err != nil {
		return nil, err
	}
	logger := &Logger{queue: make(chan []byte, options.Capacity), stop: make(chan struct{}), done: make(chan error, 1), writer: writer, now: options.Now, redactor: NewRedactor(options.Secrets...)}
	go logger.run()
	return logger, nil
}

func (logger *Logger) Emit(event Event) bool {
	if logger == nil || logger.closed.Load() || !validEvent(event) {
		return false
	}
	event = logger.redactor.Event(event)
	body, err := json.Marshal(record{Timestamp: logger.now().UTC(), Event: event})
	if err != nil {
		logger.failures.Add(1)
		return false
	}
	body = append(body, '\n')
	select {
	case logger.queue <- body:
		return true
	default:
		logger.dropped.Add(1)
		return false
	}
}

func (logger *Logger) Close(ctx context.Context) error {
	if logger == nil || !logger.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(logger.stop)
	select {
	case err := <-logger.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (logger *Logger) Dropped() uint64 {
	if logger == nil {
		return 0
	}
	return logger.dropped.Load()
}
func (logger *Logger) Failures() uint64 {
	if logger == nil {
		return 0
	}
	return logger.failures.Load()
}
func (logger *Logger) DisplayLocation() string { return DisplayDirectory }

func (logger *Logger) run() {
	var firstError error
	recordFailure := func(err error) {
		if err == nil {
			return
		}
		logger.failures.Add(1)
		if firstError == nil {
			firstError = err
		}
	}
	finish := func() {
		recordFailure(logger.writer.sync())
		recordFailure(logger.writer.close())
		logger.done <- firstError
		close(logger.done)
	}
	for {
		select {
		case body := <-logger.queue:
			recordFailure(logger.writer.write(body))
		case <-logger.stop:
			for {
				select {
				case body := <-logger.queue:
					recordFailure(logger.writer.write(body))
				default:
					finish()
					return
				}
			}
		}
	}
}

func validEvent(event Event) bool {
	switch event.Name {
	case EventStartupPhase, EventListenerSelected, EventPackageVerification, EventProcessLifecycle, EventHealthTransition, EventCapabilityTransition, EventJobRecovery, EventTaskReconciliation, EventSafeError, EventChildOutput, EventHTTPRequest, EventConfigLoaded:
	default:
		return false
	}
	if !safeToken(event.Component, 128) || event.DurationMS < 0 || !safeOptionalToken(event.Phase, 128) || !safeOptionalToken(event.State, 128) || !safeOptionalCode(event.Code) {
		return false
	}
	for _, value := range []string{event.Correlation.RootRequestID, event.Correlation.AttemptRequestID, event.Correlation.JobID, event.Correlation.TaskID, event.Correlation.ProviderRequestID} {
		if value != "" && !safeCorrelationID(value) {
			return false
		}
	}
	return len(event.Fields) <= 32
}

// Middleware establishes one bounded root request identity for the entire Eco
// request and records only the route template, never a query or raw URL.
func (logger *Logger) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := strings.TrimSpace(c.GetHeader("X-Request-ID"))
		if !safeCorrelationID(requestID) {
			requestID = uuid.NewString()
		}
		c.Request.Header.Set("X-Request-ID", requestID)
		c.Header("X-Request-ID", requestID)
		started := logger.now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		logger.Emit(Event{
			Name: EventHTTPRequest, Component: "http", State: "completed", DurationMS: logger.now().Sub(started).Milliseconds(),
			Correlation: Correlation{RootRequestID: requestID}, Fields: map[string]any{"method": c.Request.Method, "route": route, "status": c.Writer.Status()},
		})
	}
}

func safeCorrelationID(value string) bool {
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	for _, character := range value {
		if character != '-' && character != '_' && character != '.' && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func safeToken(value string, maximum int) bool {
	return value != "" && safeOptionalToken(value, maximum)
}
func safeOptionalToken(value string, maximum int) bool {
	return utf8.ValidString(value) && !strings.ContainsAny(value, "\r\n\x00") && len(value) <= maximum
}
func safeOptionalCode(value string) bool {
	if value == "" {
		return true
	}
	for _, character := range value {
		if character != '_' && (character < 'A' || character > 'Z') && (character < '0' || character > '9') {
			return false
		}
	}
	return len(value) <= 128
}

type rotatingWriter struct {
	directory, current string
	maximum            int64
	count              int
	file               *os.File
	size               int64
}

func newRotatingWriter(directory string, maximum int64, count int) (*rotatingWriter, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, err
	}
	current := filepath.Join(directory, Filename)
	file, err := os.OpenFile(current, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err = file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &rotatingWriter{directory: directory, current: current, maximum: maximum, count: count, file: file, size: info.Size()}, nil
}

func (writer *rotatingWriter) write(body []byte) error {
	if int64(len(body)) > writer.maximum {
		return ErrLoggerInvalid
	}
	if writer.size+int64(len(body)) > writer.maximum {
		if err := writer.rotate(); err != nil {
			return err
		}
	}
	written, err := writer.file.Write(body)
	writer.size += int64(written)
	return err
}

func (writer *rotatingWriter) rotate() error {
	if err := writer.file.Sync(); err != nil {
		return err
	}
	if err := writer.file.Close(); err != nil {
		return err
	}
	if writer.count == 1 {
		if err := os.Remove(writer.current); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else {
		_ = os.Remove(fmt.Sprintf("%s.%d", writer.current, writer.count-1))
	}
	for index := writer.count - 2; index >= 1; index-- {
		older := fmt.Sprintf("%s.%d", writer.current, index)
		newer := fmt.Sprintf("%s.%d", writer.current, index+1)
		if err := os.Rename(older, newer); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if writer.count > 1 {
		if err := os.Rename(writer.current, writer.current+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	file, err := os.OpenFile(writer.current, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	writer.file, writer.size = file, 0
	return nil
}

func (writer *rotatingWriter) sync() error {
	if writer == nil || writer.file == nil {
		return nil
	}
	return writer.file.Sync()
}
func (writer *rotatingWriter) close() error {
	if writer == nil || writer.file == nil {
		return nil
	}
	return writer.file.Close()
}

type Redactor struct{ audit aiaudit.Redactor }

func NewRedactor(secrets ...[]byte) Redactor { return Redactor{audit: aiaudit.NewRedactor(secrets...)} }

var (
	windowsUserPath = regexp.MustCompile(`(?i)[a-z]:\\users\\[^\\\s"']+(?:\\[^\s"']*)?`)
	unixUserPath    = regexp.MustCompile(`/(Users|home)/[^/\s]+(?:/[^\s"']*)?`)
	sqlText         = regexp.MustCompile(`(?i)\b(select|insert|update|delete|create|alter|drop)\b.{0,256}\b(from|into|table|set)\b`)
)

func (redactor Redactor) Event(event Event) Event {
	event.Fields = redactor.Fields(event.Fields)
	return event
}

func (redactor Redactor) Fields(fields map[string]any) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	result := make(map[string]any, len(fields))
	for key, value := range fields {
		if !allowedField(key) {
			continue
		}
		if forbiddenField(key) {
			result[key] = aiaudit.Redacted
			continue
		}
		result[key] = redactor.value(key, value)
	}
	return result
}

func (redactor Redactor) value(key string, value any) any {
	value = normalizeJSONValue(value)
	sanitized := redactor.audit.Value(value)
	switch current := sanitized.(type) {
	case string:
		current = strings.ReplaceAll(current, "\x00", "")
		current = windowsUserPath.ReplaceAllString(current, "<path>")
		current = unixUserPath.ReplaceAllString(current, "<path>")
		if sqlText.MatchString(current) {
			return "<redacted-sql>"
		}
		if len(current) > 2048 {
			current = current[:2048]
			for !utf8.ValidString(current) {
				current = current[:len(current)-1]
			}
		}
		return current
	case map[string]any:
		return redactor.Fields(current)
	case []any:
		result := make([]any, len(current))
		for index := range current {
			result[index] = redactor.value(key, current[index])
		}
		return result
	default:
		return current
	}
}

func normalizeJSONValue(value any) any {
	if value == nil {
		return nil
	}
	switch value.(type) {
	case string, bool, float32, float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number, map[string]any, []any:
		return value
	}
	if reflect.ValueOf(value).Kind() == reflect.Func {
		return aiaudit.Redacted
	}
	body, err := json.Marshal(value)
	if err != nil {
		return aiaudit.Redacted
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	var normalized any
	if err = decoder.Decode(&normalized); err != nil {
		return aiaudit.Redacted
	}
	return normalized
}

func allowedField(value string) bool {
	switch value {
	case "version", "build", "package_mode", "listener", "method", "route", "status", "stream", "line_bytes", "line_sha256", "truncated", "dropped_before", "config_version", "ownership", "restart_attempt", "recovery_state", "job_kind":
		return true
	default:
		return false
	}
}

func forbiddenField(value string) bool {
	value = strings.ToLower(strings.NewReplacer("-", "_", " ", "_", ".", "_").Replace(value))
	for _, fragment := range []string{"authorization", "credential", "password", "secret", "token", "cookie", "provider_body", "raw_body", "embedding", "graph_text", "business_text", "entity_payload", "sql", "stack", "debug_dump"} {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}

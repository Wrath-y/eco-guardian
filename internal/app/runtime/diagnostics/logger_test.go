package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/app/runtime/graphprocess"
	platformprocess "github.com/zouyi/eco-guardian/internal/platform/process"
)

func TestRedactorGoldenDropsUnknownFieldsAndRemovesSensitiveValues(t *testing.T) {
	redactor := NewRedactor([]byte("fixture-secret"))
	fields := redactor.Fields(map[string]any{
		"build":          "Bearer fixture-secret /Users/alice/private/project.db",
		"listener":       `C:\Users\alice\EcoGuardian\runtime`,
		"config_version": map[string]string{"password": "fixture-secret", "safe": "v1"},
		"raw_payload":    "business-canary",
	})
	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, forbidden := range []string{"fixture-secret", "alice", "private", "business-canary", "raw_payload"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("redacted output contains %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, `\u003cpath\u003e`) || !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("redacted output=%s", text)
	}
}

func FuzzRedactorNeverEmitsKnownSecretOrRawUserPath(f *testing.F) {
	f.Add("normal")
	f.Add("SELECT * FROM private_table")
	f.Add("界面")
	f.Fuzz(func(t *testing.T, input string) {
		redactor := NewRedactor([]byte("fixture-secret"))
		fields := redactor.Fields(map[string]any{"build": input + " fixture-secret /Users/alice/private"})
		body, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if strings.Contains(text, "fixture-secret") || strings.Contains(text, "/Users/alice") || strings.Contains(text, "\x00") {
			t.Fatalf("unsafe output=%q", text)
		}
	})
}

func TestLoggerRotatesReopensFlushesAndCorrelatesHTTP(t *testing.T) {
	directory := t.TempDir()
	newLogger := func() *Logger {
		logger, err := New(Options{Directory: directory, MaxBytes: 64 << 10, MaxFiles: 3, Capacity: 256, Now: time.Now})
		if err != nil {
			t.Fatal(err)
		}
		return logger
	}
	logger := newLogger()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(logger.Middleware())
	engine.GET("/safe/:id", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/safe/business-secret?authorization=fixture-secret", nil)
	request.Header.Set("X-Request-ID", "root-request_1")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Header().Get("X-Request-ID") != "root-request_1" {
		t.Fatalf("request id=%q", response.Header().Get("X-Request-ID"))
	}
	for index := 0; index < 100; index++ {
		logger.Emit(Event{Name: EventProcessLifecycle, Component: "runtime", Phase: "running", Fields: map[string]any{"build": strings.Repeat("x", 1800)}})
	}
	if err := logger.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 || len(entries) > 3 {
		t.Fatalf("rotated files=%d", len(entries))
	}
	for _, entry := range entries {
		info, statErr := entry.Info()
		if statErr != nil || info.Size() > 64<<10 {
			t.Fatalf("entry=%s info=%v err=%v", entry.Name(), info, statErr)
		}
	}
	logger = newLogger()
	logger.Emit(Event{Name: EventProcessLifecycle, Component: "runtime", Phase: "reopened"})
	if err = logger.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(directory, Filename))
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	if !strings.Contains(text, `"phase":"reopened"`) || strings.Contains(text, "business-secret") || strings.Contains(text, "fixture-secret") {
		t.Fatalf("log contents=%s", text)
	}
}

func TestLoggerFailureIsolationAndBackpressure(t *testing.T) {
	notDirectory := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDirectory, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{Directory: notDirectory, MaxBytes: 64 << 10, MaxFiles: 2, Capacity: 1}); err == nil {
		t.Fatal("expected unwritable log location failure")
	}

	logger, err := New(Options{Directory: t.TempDir(), MaxBytes: 64 << 10, MaxFiles: 2, Capacity: 1})
	if err != nil {
		t.Fatal(err)
	}
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = logger.writer.file.Close(); err != nil {
		t.Fatal(err)
	}
	logger.writer.file = writePipe
	logger.writer.maximum = 1 << 30
	logger.writer.size = 0
	for index := 0; index < 1000 && logger.Dropped() == 0; index++ {
		logger.Emit(Event{Name: EventProcessLifecycle, Component: "runtime", Phase: "flood", Fields: map[string]any{"build": strings.Repeat("x", 2048)}})
	}
	if logger.Dropped() == 0 {
		t.Fatal("flood did not trigger bounded-queue backpressure")
	}
	go func() { _, _ = io.Copy(io.Discard, readPipe); _ = readPipe.Close() }()
	_ = logger.Close(context.Background())

	failed, err := New(Options{Directory: t.TempDir(), MaxBytes: 64 << 10, MaxFiles: 2, Capacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err = failed.writer.file.Close(); err != nil {
		t.Fatal(err)
	}
	failed.Emit(Event{Name: EventSafeError, Component: "diagnostics", Code: "WRITE_FAILED"})
	if err = failed.Close(context.Background()); err == nil || failed.Failures() == 0 {
		t.Fatalf("close error=%v failures=%d", err, failed.Failures())
	}
}

func TestChildSinkNeverPersistsRawChildLine(t *testing.T) {
	directory := t.TempDir()
	logger, err := New(Options{Directory: directory, MaxBytes: 64 << 10, MaxFiles: 2, Capacity: 4, Secrets: [][]byte{[]byte("fixture-secret")}})
	if err != nil {
		t.Fatal(err)
	}
	ChildSink{Logger: logger}.PublishChildEvent(graphprocess.ChildOutputEvent{
		Component: "local-rag", Stream: platformprocess.StreamStderr, Generation: 7,
		Line: `{"authorization":"Bearer fixture-secret","graph":"business-payload","embedding":[1,2,3],"sql":"SELECT * FROM private"}`,
	})
	if err = logger.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(directory, Filename))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"fixture-secret", "business-payload", "embedding", "SELECT", "private"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("child log contains %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(string(body), `"launch_generation":7`) || !strings.Contains(string(body), `"line_sha256"`) {
		t.Fatalf("missing safe child metadata: %s", body)
	}
}

func TestLoggerRejectsInvalidEventsWithoutWriting(t *testing.T) {
	logger, err := New(Options{Directory: t.TempDir(), MaxBytes: 64 << 10, MaxFiles: 2, Capacity: 2})
	if err != nil {
		t.Fatal(err)
	}
	if logger.Emit(Event{Name: EventName("invented"), Component: "runtime"}) || logger.Emit(Event{Name: EventJobRecovery, Component: "runtime", Correlation: Correlation{JobID: "bad/id"}}) {
		t.Fatal("invalid event accepted")
	}
	if err = logger.Close(context.Background()); err != nil && !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
}

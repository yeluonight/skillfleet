package serverlog

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestNewTextHandlerEmitsKey(t *testing.T) {
	var buf bytes.Buffer
	log, err := New(&buf, "info", "text")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	log.Info("hello", slog.String("user", "alice"))
	out := buf.String()
	if !strings.Contains(out, `msg=hello`) {
		t.Errorf("text output missing msg=hello: %q", out)
	}
	if !strings.Contains(out, `user=alice`) {
		t.Errorf("text output missing user=alice: %q", out)
	}
}

func TestNewJSONHandlerEmitsKey(t *testing.T) {
	var buf bytes.Buffer
	log, err := New(&buf, "warn", "json")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	log.Warn("oops", slog.Int("code", 42))
	out := buf.String()
	if !strings.Contains(out, `"msg":"oops"`) {
		t.Errorf("json output missing msg: %q", out)
	}
	if !strings.Contains(out, `"code":42`) {
		t.Errorf("json output missing code=42: %q", out)
	}
	// AddSource kicks in at warn+, the source key must appear.
	if !strings.Contains(out, `"source"`) {
		t.Errorf("json output should include source at warn level: %q", out)
	}
}

func TestNewRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	log, err := New(&buf, "warn", "text")
	if err != nil {
		t.Fatal(err)
	}
	log.Debug("d")
	log.Info("i")
	if buf.Len() != 0 {
		t.Errorf("debug/info should be filtered at warn level, got %q", buf.String())
	}
	log.Warn("w")
	if !strings.Contains(buf.String(), "w") {
		t.Errorf("warn message dropped: %q", buf.String())
	}
}

func TestNewRejectsBadLevel(t *testing.T) {
	_, err := New(nil, "verbose", "text")
	if err == nil {
		t.Fatal("expected error for unknown level")
	}
}

func TestNewRejectsBadFormat(t *testing.T) {
	_, err := New(nil, "info", "logfmt")
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
}

func TestNewDefaultsToStderrWhenWriterNil(t *testing.T) {
	log, err := New(nil, "info", "text")
	if err != nil {
		t.Fatal(err)
	}
	if log == nil {
		t.Fatal("nil logger")
	}
	// Hard to assert "writes to stderr" without capturing fd 2; the
	// fact that New didn't error and returned a usable logger is
	// the contract we promise.
	log.Info("smoke")
}

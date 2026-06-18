// Package serverlog wires SkillFleet's structured logging.
//
// The server reads logging.level and logging.format from the YAML
// config and asks New for a *slog.Logger. Splitting this from main
// keeps tests cheap (no main reachability problem) and gives us one
// place to evolve handler choice later.
package serverlog

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// New returns a logger that writes to w with the given level and
// format. Format must be "text" or "json"; level must be one of
// debug|info|warn|error (case-insensitive). w == nil means os.Stderr.
//
// The handler adds a "source" attribute for warn/error levels so
// production logs can be traced back to the call site without paying
// the cost on the hot info path.
func New(w io.Writer, level, format string) (*slog.Logger, error) {
	if w == nil {
		w = os.Stderr
	}
	lv, err := parseLevel(level)
	if err != nil {
		return nil, err
	}
	opts := &slog.HandlerOptions{
		Level:     lv,
		AddSource: lv >= slog.LevelWarn,
	}
	var h slog.Handler
	switch strings.ToLower(format) {
	case "text", "":
		h = slog.NewTextHandler(w, opts)
	case "json":
		h = slog.NewJSONHandler(w, opts)
	default:
		return nil, fmt.Errorf("serverlog: unknown format %q (want text|json)", format)
	}
	return slog.New(h), nil
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("serverlog: unknown level %q (want debug|info|warn|error)", s)
	}
}

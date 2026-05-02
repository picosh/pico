// Package prettylog provides a human-friendly slog handler with colors,
// aligned levels, and one-attribute-per-line formatting.
package prettylog

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

// ANSI color codes.
const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorCyan   = "\033[36m"
	colorDim    = "\033[2m"
)

// Handler is a slog.Handler that renders log records in a human-friendly format.
type Handler struct {
	attrs     []slog.Attr
	group     string // group prefix for keys
	level     slog.Level
	write     io.Writer
	useColor  bool
	showTime  bool
	showLevel bool
}

// Options configure the pretty handler.
type Options struct {
	// Level controls the minimum log level. Defaults to slog.LevelInfo.
	Level slog.Level
	// UseColor enables ANSI color codes. Defaults to true when stdout is a TTY.
	UseColor *bool
	// ShowTime includes the time on each line. Defaults to true.
	ShowTime bool
	// ShowLevel includes the level badge. Defaults to true.
	ShowLevel bool
}

// New creates a new pretty handler writing to w.
func New(w io.Writer, opts Options) *Handler {
	useColor := true
	if opts.UseColor != nil {
		useColor = *opts.UseColor
	} else if f, ok := w.(*os.File); ok {
		useColor = term.IsTerminal(int(f.Fd()))
	}
	return &Handler{
		write:     w,
		useColor:  useColor,
		level:     opts.Level,
		showTime:  true,
		showLevel: true,
	}
}

// Enabled returns true if the given level is at or above the handler's minimum level.
func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

// WithAttrs returns a new handler with additional attributes.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	newAttrs = append(newAttrs, h.attrs...)
	newAttrs = append(newAttrs, attrs...)
	return &Handler{
		attrs:     newAttrs,
		level:     h.level,
		write:     h.write,
		useColor:  h.useColor,
		showTime:  h.showTime,
		showLevel: h.showLevel,
	}
}

// WithGroup returns a new handler with a group prefix.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	prefix := h.group
	if prefix != "" {
		prefix = prefix + "." + name
	} else {
		prefix = name
	}
	return &Handler{
		attrs:     h.attrs,
		group:     prefix,
		level:     h.level,
		write:     h.write,
		useColor:  h.useColor,
		showTime:  h.showTime,
		showLevel: h.showLevel,
	}
}

// Handle formats and writes a log record.
func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	var sb strings.Builder

	// Time
	if h.showTime {
		sb.WriteString(r.Time.Format("15:04:05"))
		sb.WriteString("  ")
	}

	// Level badge
	if h.showLevel {
		sb.WriteString(levelString(r.Level, h.useColor))
		sb.WriteString("  ")
	}

	// Message
	sb.WriteString(r.Message)
	sb.WriteString("\n")

	// Write the main line
	if _, err := h.write.Write([]byte(sb.String())); err != nil {
		return err
	}

	// Collect all attributes (handler-level + record-level).
	var allAttrs []slog.Attr
	allAttrs = append(allAttrs, h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		if h.group != "" {
			a.Key = h.group + "." + a.Key
		}
		allAttrs = append(allAttrs, a)
		return true
	})

	// Write each attribute on its own indented line.
	prefix := strings.Repeat(" ", 8)
	for _, a := range allAttrs {
		line := prefix + attrLine(a, h.useColor) + "\n"
		if _, err := h.write.Write([]byte(line)); err != nil {
			return err
		}
	}

	return nil
}

// levelString returns the formatted level string, optionally with color.
func levelString(level slog.Level, useColor bool) string {
	label := levelLabel(level)
	if !useColor {
		return label
	}
	switch {
	case level >= slog.LevelError:
		return colorRed + colorBold + label + colorReset
	case level >= slog.LevelWarn:
		return colorYellow + colorBold + label + colorReset
	case level >= slog.LevelInfo:
		return colorGreen + label + colorReset
	default:
		return colorDim + label + colorReset
	}
}

func levelLabel(level slog.Level) string {
	switch {
	case level >= slog.LevelError:
		return "ERROR"
	case level >= slog.LevelWarn:
		return "WARN "
	case level >= slog.LevelInfo:
		return "INFO "
	default:
		return "DEBUG"
	}
}

// attrLine formats a single attribute as "key=value".
func attrLine(a slog.Attr, useColor bool) string {
	key := colorKey(a.Key, useColor)
	val := formatValue(a.Value, useColor)
	return fmt.Sprintf("%s=%s", key, val)
}

func colorKey(key string, useColor bool) string {
	if !useColor {
		return key
	}
	return colorCyan + key + colorReset
}

func formatValue(v slog.Value, useColor bool) string {
	s := valueString(v)
	if !useColor {
		return s
	}
	// Color errors red.
	if err, ok := v.Any().(error); ok {
		return colorRed + err.Error() + colorReset
	}
	if v.Kind() == slog.KindString && strings.Contains(s, "\n") {
		return colorDim + s + colorReset
	}
	return s
}

func valueString(v slog.Value) string {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindInt64:
		return strconv.FormatInt(v.Int64(), 10)
	case slog.KindUint64:
		return strconv.FormatUint(v.Uint64(), 10)
	case slog.KindFloat64:
		return strconv.FormatFloat(v.Float64(), 'f', -1, 64)
	case slog.KindBool:
		return strconv.FormatBool(v.Bool())
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindTime:
		return v.Time().Format(time.Kitchen)
	default:
		return fmt.Sprintf("%v", v.Any())
	}
}

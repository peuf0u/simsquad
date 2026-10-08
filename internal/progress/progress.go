// Package progress emits one human-readable event per long-running operation
// to stderr. Stdout is reserved for the final JSON contract; nothing in this
// package may write there.
//
// The Logger fronts a buffered channel drained by a single goroutine, so
// callers from arbitrary goroutines never serialise on stderr writes nor tear
// each other's lines.
package progress

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/peuf0u/simsquad/internal/tui"
)

// Level classifies the severity of an event.
type Level int

// Levels — kept as a small enum so the styling table in render() is exhaustive.
const (
	LevelInfo Level = iota
	LevelSuccess
	LevelWarn
	LevelError
)

// Field is a single key=value annotation on an event line. Use F() to build
// these inline.
type Field struct {
	Key   string
	Value any
}

// F is a sugar constructor for inline field lists: progress.F("path", p).
func F(key string, value any) Field {
	return Field{Key: key, Value: value}
}

// Event is one drained log line.
type Event struct {
	When    time.Time
	Level   Level
	Message string
	Fields  []Field
}

// Logger is a buffered, goroutine-safe progress sink. Use New to construct
// one, Close (or Stop) to drain pending events before the process exits.
type Logger struct {
	w            io.Writer
	ch           chan Event
	done         chan struct{}
	startMu      sync.Once
	closeMu      sync.Once
	startedDrain bool
	started      time.Time
}

// New returns a Logger that writes to stderr. Callers can override the writer
// (e.g. for tests) via NewWith.
func New() *Logger { return NewWith(os.Stderr) }

// NewWith returns a Logger writing to w. The goroutine starts lazily on the
// first Log call so a Logger that's never used costs nothing.
func NewWith(w io.Writer) *Logger {
	return &Logger{
		w:    w,
		ch:   make(chan Event, 128),
		done: make(chan struct{}),
	}
}

func (l *Logger) ensureRunning() {
	l.startMu.Do(func() {
		l.startedDrain = true
		l.started = time.Now()
		go l.drain()
	})
}

func (l *Logger) drain() {
	for ev := range l.ch {
		_, _ = fmt.Fprintln(l.w, l.render(ev))
	}
	close(l.done)
}

// Close flushes any buffered events and stops the drain goroutine. It is safe
// to call more than once. After Close, further Log calls panic — by design;
// Close marks shutdown.
func (l *Logger) Close() {
	l.closeMu.Do(func() {
		if !l.startedDrain {
			return
		}
		close(l.ch)
		<-l.done
	})
}

// Log emits one event at the given level. If the buffer is full, Log drops
// the event rather than blocking; the JSON contract on stdout is always more
// important than complete progress output.
func (l *Logger) Log(level Level, msg string, fields ...Field) {
	l.ensureRunning()
	ev := Event{When: time.Now(), Level: level, Message: msg, Fields: fields}
	select {
	case l.ch <- ev:
	default:
		// Channel full; drop quietly. Better than blocking a build worker
		// because stderr is slow.
	}
}

// Info is a sugar for Log(LevelInfo, …).
func (l *Logger) Info(msg string, fields ...Field) { l.Log(LevelInfo, msg, fields...) }

// Success is a sugar for Log(LevelSuccess, …).
func (l *Logger) Success(msg string, fields ...Field) { l.Log(LevelSuccess, msg, fields...) }

// Warn is a sugar for Log(LevelWarn, …).
func (l *Logger) Warn(msg string, fields ...Field) { l.Log(LevelWarn, msg, fields...) }

// Errorf is a sugar for Log(LevelError, fmt.Sprintf(…)).
func (l *Logger) Errorf(format string, args ...any) {
	l.Log(LevelError, fmt.Sprintf(format, args...))
}

// Step emits "<event>: start", then returns a finisher to call when the
// operation completes. The finisher emits "<event>: done" with a took=… field
// on success, or "<event>: error" with the error message on failure (when err
// is non-nil at call time).
//
// Usage:
//
//	done := log.Step("ios-build", progress.F("repo", repoDir))
//	err := buildIOS(...)
//	done(err)
func (l *Logger) Step(event string, fields ...Field) func(error) {
	l.Info(event+": start", fields...)
	start := time.Now()
	return func(err error) {
		took := time.Since(start).Truncate(100 * time.Millisecond)
		ff := append([]Field{F("took", took.String())}, fields...)
		if err != nil {
			ff = append(ff, F("error", err.Error()))
			l.Log(LevelError, event+": error", ff...)
			return
		}
		l.Log(LevelSuccess, event+": done", ff...)
	}
}

func (l *Logger) render(ev Event) string {
	stamp := ev.When.Sub(l.started).Round(100 * time.Millisecond).String()

	var icon, levelStr string
	switch ev.Level {
	case LevelSuccess:
		icon = tui.StyleSuccess.Render(tui.IconSuccess)
		levelStr = tui.StyleSuccess.Render(ev.Message)
	case LevelWarn:
		icon = tui.StyleWarn.Render(tui.IconWarn)
		levelStr = tui.StyleWarn.Render(ev.Message)
	case LevelError:
		icon = tui.StyleError.Render(tui.IconError)
		levelStr = tui.StyleError.Render(ev.Message)
	default:
		icon = tui.StyleInfo.Render(tui.IconInfo)
		levelStr = ev.Message
	}

	var b strings.Builder
	b.WriteString(tui.StyleDim.Render(fmt.Sprintf("[%-5s]", stamp)))
	b.WriteByte(' ')
	b.WriteString(icon)
	b.WriteByte(' ')
	b.WriteString(levelStr)
	for _, f := range ev.Fields {
		b.WriteByte(' ')
		b.WriteString(tui.StyleDim.Render(fmt.Sprintf("%s=%v", f.Key, f.Value)))
	}
	return b.String()
}

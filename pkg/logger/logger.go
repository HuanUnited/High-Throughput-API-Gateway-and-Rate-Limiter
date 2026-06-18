// Package logger provides a minimal, production-ready logging wrapper
// around the standard library's log/slog.
package logger

import (
	"io"
	"log/slog"
	"os"
)

// Options configures the logger.
type Options struct {
	Level  slog.Level
	Output io.Writer
	JSON   bool // if true, emit JSON logs; otherwise text
}

// New creates a slog.Logger from options.
func New(opts Options) *slog.Logger {
	if opts.Output == nil {
		opts.Output = os.Stdout
	}

	handlerOpts := &slog.HandlerOptions{Level: opts.Level}

	var handler slog.Handler
	if opts.JSON {
		handler = slog.NewJSONHandler(opts.Output, handlerOpts)
	} else {
		handler = slog.NewTextHandler(opts.Output, handlerOpts)
	}

	return slog.New(handler)
}

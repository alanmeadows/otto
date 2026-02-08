package logging

import (
	"log/slog"
	"os"

	charmlog "github.com/charmbracelet/log"
	"golang.org/x/term"
)

// Setup initializes the global slog logger using charmbracelet/log as the backend.
// If the output is a terminal, uses colored text format. Otherwise, uses JSON format.
func Setup(verbose bool) {
	handler := charmlog.NewWithOptions(os.Stderr, charmlog.Options{
		ReportTimestamp: true,
	})

	if verbose {
		handler.SetLevel(charmlog.DebugLevel)
	} else {
		handler.SetLevel(charmlog.InfoLevel)
	}

	// Use plain format for non-TTY output
	if !isTerminal() {
		handler.SetFormatter(charmlog.JSONFormatter)
	}

	slog.SetDefault(slog.New(handler))
}

func isTerminal() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}

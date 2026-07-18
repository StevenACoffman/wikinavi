// Package root defines the root configuration for the CLI.
package root

import (
	"fmt"
	"io"
	"log/slog"

	"github.com/peterbourgon/ff/v4"
)

// ExitError is returned by commands that want a specific non-zero exit code
// without printing an additional error message. run() in main.go checks for
// ExitError with errors.As and calls os.Exit(int(e)) directly, bypassing the
// default "error: ..." printer.
type ExitError int

func (e ExitError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// Config holds shared I/O writers, shared flags, and the root ff.Command.
// All subcommand configs embed *Config to inherit these. Logger is nil until
// the dispatcher constructs it (post-parse, once Verbose is known) and assigns
// it here; subcommands read it via the embedded *Config.
type Config struct {
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Verbose bool
	Logger  *slog.Logger
	Flags   *ff.FlagSet
	Command *ff.Command
}

// New returns a new root Config with the given I/O writers.
func New(stdin io.Reader, stdout, stderr io.Writer) *Config {
	var cfg Config
	cfg.Stdin = stdin
	cfg.Stdout = stdout
	cfg.Stderr = stderr
	// --verbose is a shared flag: subcommands inherit it via SetParent(parent.Flags).
	cfg.Flags = ff.NewFlagSet("wikinavi")
	cfg.Flags.BoolVar(&cfg.Verbose, 'v', "verbose", "enable verbose (debug) logging")
	cfg.Command = &ff.Command{
		Name:      "wikinavi",
		Usage:     "wikinavi <SUBCOMMAND> ...",
		ShortHelp: "generate wiki navigation (table of contents) for GitHub wikis",
		Flags:     cfg.Flags,
	}
	return &cfg
}

// NewLogger builds the shared slog logger. It writes to w (typically stderr)
// at Info level, or Debug level when verbose is true. The timestamp attribute
// is dropped because it is noise for an interactive CLI.
func NewLogger(w io.Writer, verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))
}

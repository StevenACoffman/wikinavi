// Package cmd is the dispatcher for the wikinavi CLI.
// It registers all commands and routes incoming arguments
// to the matching command implementation.
package cmd

// climax:name wikinavi
// climax:root-pkg root
// climax:env-prefix WIKINAVI

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/peterbourgon/ff/v4"
	"github.com/peterbourgon/ff/v4/ffhelp"

	"github.com/StevenACoffman/wikinavi/cmd/gen"
	"github.com/StevenACoffman/wikinavi/cmd/root"
	"github.com/StevenACoffman/wikinavi/cmd/version"
	// climax:imports
)

// Run parses args and dispatches to the matching command.
// args must not include the executable name (pass os.Args[1:]).
//
// Every flag can be set via a WIKINAVI_-prefixed environment variable.
// The mapping rule is: prepend WIKINAVI_, uppercase, replace dashes with
// underscores.
//
// Flags supplied on the command line always take precedence over env vars.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	r := root.New(stdin, stdout, stderr)
	version.New(r)
	gen.New(r)
	// register new commands here

	if err := r.Command.Parse(args, ff.WithEnvVarPrefix("WIKINAVI")); err != nil {
		_, _ = fmt.Fprintf(stderr, "\n%s\n", ffhelp.Command(r.Command))
		return fmt.Errorf("parse: %w", err)
	}

	// Post-parse initialization: --verbose is now known, so build the shared
	// logger and hand it to every subcommand via the embedded *root.Config.
	r.Logger = root.NewLogger(stderr, r.Verbose)

	if err := r.Command.Run(ctx); err != nil {
		// Don't print usage help for ErrNoExec (no subcommand given) or
		// ExitError (command already reported its own outcome).
		var exitErr root.ExitError
		if !errors.Is(err, ff.ErrNoExec) && !errors.As(err, &exitErr) {
			_, _ = fmt.Fprintf(stderr, "\n%s\n", ffhelp.Command(r.Command.GetSelected()))
		}
		return err
	}

	return nil
}

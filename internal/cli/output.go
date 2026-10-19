package cli

import (
	"fmt"
	"io"
	"os"
)

// Glyphs used by the output contract in build spec section 7. Go source is
// UTF-8 by definition, so these are written as literals; there is deliberately
// no terminal-capability detection shim.
const (
	glyphCheck = "✔" // heavy check mark: a completed step
	glyphArrow = "→" // rightwards arrow: a resource the user can reach
	glyphCross = "✗" // ballot X: the step that failed
	glyphWarn  = "!" // warning marker (the spec leaves this one unspecified)
)

// out receives normal CLI output. It is a variable so tests can capture what
// the printers emit; production code should never reassign it.
var out io.Writer = os.Stdout

// errOut receives warnings and the final failure line, mirroring out.
var errOut io.Writer = os.Stderr

// readyLine is the exact closing line of a successful run (spec section 7).
const readyLine = "  Ready. Run 'loam down' when finished."

// Step reports a completed step, rendered as "  <check> <message>".
func Step(format string, a ...any) {
	fmt.Fprintf(out, "  %s %s\n", glyphCheck, fmt.Sprintf(format, a...))
}

// Resource reports something the developer can now use - an app URL, a masked
// database URL - rendered as "  <arrow> <message>".
func Resource(format string, a ...any) {
	fmt.Fprintf(out, "  %s %s\n", glyphArrow, fmt.Sprintf(format, a...))
}

// Warn reports a non-fatal problem on the error stream. The run continues.
func Warn(format string, a ...any) {
	fmt.Fprintf(errOut, "  %s %s\n", glyphWarn, fmt.Sprintf(format, a...))
}

// Failf wraps err into an error that names the step that failed and carries an
// actionable hint (spec section 7 failure rule). It prints nothing: Execute is
// the single place that renders errors, so callers can return this value up the
// stack, add context, or inspect it with errors.Is/errors.As.
func Failf(step string, err error, hint string) error {
	switch {
	case err == nil && hint == "":
		return fmt.Errorf("%s failed", step)
	case err == nil:
		return fmt.Errorf("%s failed\n  hint: %s", step, hint)
	case hint == "":
		return fmt.Errorf("%s failed: %w", step, err)
	default:
		return fmt.Errorf("%s failed: %w\n  hint: %s", step, err, hint)
	}
}

// Ready prints the closing line of a successful up.
func Ready() {
	fmt.Fprintln(out, readyLine)
}

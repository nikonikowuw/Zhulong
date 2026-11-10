package algorunner

import (
	"fmt"
	"os"
)

// runnerPipes holds both output pipes of one spawn so every failure path can
// close them through a single call.
type runnerPipes struct {
	stdoutR *os.File
	stdoutW *os.File
	stderrR *os.File
	stderrW *os.File
}

// openRunnerPipes creates the stdout/stderr pipe pair for one spawn.
func openRunnerPipes() (*runnerPipes, error) {
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		closePipes(stdoutR, stdoutW)
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}
	return &runnerPipes{stdoutR: stdoutR, stdoutW: stdoutW, stderrR: stderrR, stderrW: stderrW}, nil
}

// close closes every pipe end of a spawn.
func (pipes *runnerPipes) close() {
	closePipes(
		pipes.stdoutR,
		pipes.stdoutW,
		pipes.stderrR,
		pipes.stderrW,
	)
}

// closePipes closes whatever is non-nil. Close errors are deliberately ignored:
// these descriptors belong to a spawn that is already failing, and a close error
// cannot change that outcome.
func closePipes(files ...*os.File) {
	for _, file := range files {
		closeFileBestEffort(file)
	}
}

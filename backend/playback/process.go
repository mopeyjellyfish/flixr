package playback

import (
	"io"
	"os"
	"os/exec"
)

// Process is the narrow process lifetime owned by a playback generation.
type Process interface {
	Signal(os.Signal) error
	Kill() error
	Wait() error
}

// Executor starts only commands fully constructed by Playback.
type Executor interface {
	Start(name string, args []string, stderr io.Writer) (Process, error)
}

type OSExecutor struct{}

type execProcess struct{ cmd *exec.Cmd }

func (p execProcess) Signal(signal os.Signal) error { return p.cmd.Process.Signal(signal) }
func (p execProcess) Kill() error                   { return p.cmd.Process.Kill() }
func (p execProcess) Wait() error                   { return p.cmd.Wait() }

func (OSExecutor) Start(name string, args []string, stderr io.Writer) (Process, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return execProcess{cmd: cmd}, nil
}

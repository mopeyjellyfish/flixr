package playback

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"sync"
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

var ffmpegReadrateCatchupSupport struct {
	sync.Once
	supported bool
}

func (p execProcess) Signal(signal os.Signal) error { return p.cmd.Process.Signal(signal) }
func (p execProcess) Kill() error                   { return p.cmd.Process.Kill() }
func (p execProcess) Wait() error                   { return p.cmd.Wait() }

func (OSExecutor) Start(name string, args []string, stderr io.Writer) (Process, error) {
	if name == "ffmpeg" {
		ffmpegReadrateCatchupSupport.Do(func() {
			output, err := exec.Command(name, "-hide_banner", "-h", "full").CombinedOutput()
			ffmpegReadrateCatchupSupport.supported = err == nil && bytes.Contains(output, []byte("-readrate_catchup"))
		})
		// FFmpeg before 8 catches up an initial read burst without a separate
		// rate option. Newer releases default catch-up to 1.05x, so they need
		// the explicit bounded rate to make the startup allowance effective.
		args = ffmpegArgsForCatchupSupport(args, ffmpegReadrateCatchupSupport.supported)
	}
	cmd := exec.Command(name, args...)
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return execProcess{cmd: cmd}, nil
}

func ffmpegArgsForCatchupSupport(args []string, supported bool) []string {
	if supported {
		return args
	}
	compatible := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		if args[index] == "-readrate_catchup" && index+1 < len(args) {
			index++
			continue
		}
		compatible = append(compatible, args[index])
	}
	return compatible
}

package stream

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

// Encoder is the backend-agnostic streaming encoder contract.
//
// The interface is intentionally small so it can be extracted into a shared
// NextUI library later without dragging the rest of the Pak-specific UI/server
// code with it.
type Encoder interface {
	Name() string
	Start(writer io.Writer) error
	Stop()
	Wait() error
}

// EncoderFactory builds a fresh encoder instance for a new client session.
type EncoderFactory func() (Encoder, error)

// execCmdFactory builds an exec.Cmd for encoder backends that still use an
// external process.
type execCmdFactory func() (*exec.Cmd, error)

type execCmdEncoder struct {
	name    string
	factory execCmdFactory

	mu  sync.Mutex
	cmd *exec.Cmd
}

func NewExecCmdEncoder(name string, factory execCmdFactory) Encoder {
	return &execCmdEncoder{name: name, factory: factory}
}

func (e *execCmdEncoder) Name() string {
	if e == nil || e.name == "" {
		return "encoder"
	}
	return e.name
}

func (e *execCmdEncoder) Start(writer io.Writer) error {
	if writer == nil {
		return fmt.Errorf("encoder writer is nil")
	}
	if e == nil || e.factory == nil {
		return fmt.Errorf("encoder factory not configured")
	}

	e.Stop()

	cmd, err := e.factory()
	if err != nil {
		return err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return err
	}

	e.mu.Lock()
	e.cmd = cmd
	e.mu.Unlock()

	go func() {
		_, _ = io.Copy(writer, stdout)
	}()

	return nil
}

func (e *execCmdEncoder) Stop() {
	if e == nil {
		return
	}

	e.mu.Lock()
	cmd := e.cmd
	e.cmd = nil
	e.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return
	}

	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

func (e *execCmdEncoder) Wait() error {
	if e == nil {
		return nil
	}

	e.mu.Lock()
	cmd := e.cmd
	e.mu.Unlock()

	if cmd == nil {
		return nil
	}

	return cmd.Wait()
}

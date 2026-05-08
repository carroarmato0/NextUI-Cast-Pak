package stream

import "os/exec"

type CmdRunner func(name string, args ...string) error

func defaultRunner(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

var alsaCandidates = []string{"hw:0,0", "default", "plughw:0,0"}

// ProbeALSA returns the first ALSA device that arecord can open, or "".
// Pass nil to use the real exec runner.
func ProbeALSA(runner CmdRunner) string {
	if runner == nil {
		runner = defaultRunner
	}
	for _, dev := range alsaCandidates {
		if err := runner("arecord", "-D", dev, "-d", "1", "-f", "S16_LE", "/dev/null"); err == nil {
			return dev
		}
	}
	return ""
}

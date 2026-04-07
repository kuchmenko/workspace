package alias

import "os/exec"

// ShellConflict reports whether `name` would shadow an existing executable on PATH.
// We only check executables — detecting shell-defined aliases/functions would
// require sourcing the user's rc file, which is too fragile to do here.
func ShellConflict(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", false
	}
	return path, true
}

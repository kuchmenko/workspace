package alias

import "os/exec"

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

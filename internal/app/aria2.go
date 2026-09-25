package app

import (
	"os/exec"
	"strings"
)

type aria2Status struct {
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
}

func detectAria2() aria2Status {
	return detectAria2At("aria2c")
}

func detectAria2At(name string) aria2Status {
	path, err := exec.LookPath(name)
	if err != nil {
		return aria2Status{}
	}
	status := aria2Status{Installed: true, Path: path}
	output, err := exec.Command(path, "--version").CombinedOutput()
	if err == nil {
		for _, line := range strings.Split(string(output), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				status.Version = line
				break
			}
		}
	}
	return status
}

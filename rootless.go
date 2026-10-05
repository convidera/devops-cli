package main

import (
	"os"
	"os/exec"
	"strings"
)

// dockerRootlessEnv lets a caller force (or suppress) rootless detection,
// the same override pattern as noTUIEnv — useful for tests and for hosts
// where `docker info` is slow or unavailable but the answer is already known.
const dockerRootlessEnv = "DEVOPS_DOCKER_ROOTLESS"

// dockerRootless reports whether the local Docker daemon runs rootless, or
// with a classic userns-remap. Both remap the invoking host user to container
// uid 0, so anything the daemon creates on a bind mount — and the container's
// own default user — ends up owned by uid 0 from inside any container, not by
// whatever non-root service account an image normally runs as. A module's own
// scripts can read DEVOPS_DOCKER_ROOTLESS from the environment (see
// rootlessEnv) instead of each reimplementing this check.
func dockerRootless() bool {
	if v := os.Getenv(dockerRootlessEnv); v != "" {
		return v != "0"
	}
	out, err := exec.Command("docker", "info", "--format", "{{.SecurityOptions}}").Output()
	if err != nil {
		return false
	}
	s := string(out)
	return strings.Contains(s, "name=userns") || strings.Contains(s, "name=rootless")
}

// rootlessEnv returns the environment a module script should run with: nil
// (inherit os.Environ() as-is, exec.Cmd's default) unless the daemon is
// rootless or userns-remapped, in which case DEVOPS_DOCKER_ROOTLESS=1 is
// added so the script can react without shelling out to `docker info` itself.
func rootlessEnv() []string {
	if !dockerRootless() {
		return nil
	}
	return appendEnv(nil, dockerRootlessEnv+"=1")
}

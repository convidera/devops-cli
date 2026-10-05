package main

import "testing"

func TestDockerRootless_envOverride(t *testing.T) {
	t.Setenv(dockerRootlessEnv, "1")
	if !dockerRootless() {
		t.Errorf("dockerRootless() = false with %s=1", dockerRootlessEnv)
	}
}

func TestDockerRootless_envOverrideZero(t *testing.T) {
	t.Setenv(dockerRootlessEnv, "0")
	if dockerRootless() {
		t.Errorf("dockerRootless() = true with %s=0", dockerRootlessEnv)
	}
}

func TestRootlessEnv_nilWhenNotRootless(t *testing.T) {
	t.Setenv(dockerRootlessEnv, "0")
	if env := rootlessEnv(); env != nil {
		t.Errorf("rootlessEnv() = %v, want nil", env)
	}
}

func TestRootlessEnv_setsFlagWhenRootless(t *testing.T) {
	t.Setenv(dockerRootlessEnv, "1")
	env := rootlessEnv()
	found := false
	for _, kv := range env {
		if kv == dockerRootlessEnv+"=1" {
			found = true
		}
	}
	if !found {
		t.Errorf("rootlessEnv() = %v, want it to contain %s=1", env, dockerRootlessEnv)
	}
}

func TestAppendEnv_materializesNilEnv(t *testing.T) {
	t.Setenv("DEVOPS_CLI_TEST_MARKER", "present")
	env := appendEnv(nil, "FOO=bar")
	hasMarker, hasFoo := false, false
	for _, kv := range env {
		if kv == "DEVOPS_CLI_TEST_MARKER=present" {
			hasMarker = true
		}
		if kv == "FOO=bar" {
			hasFoo = true
		}
	}
	if !hasMarker {
		t.Errorf("appendEnv(nil, ...) dropped the inherited environment")
	}
	if !hasFoo {
		t.Errorf("appendEnv(nil, %q) = %v, missing the appended entry", "FOO=bar", env)
	}
}

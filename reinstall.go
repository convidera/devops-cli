package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
)

const releaseBaseURL = "https://github.com/convidera/devops-cli/releases/latest/download"

// runReinstall downloads the latest release binary for the current platform
// and replaces the currently running executable with it.
func runReinstall() int {
	asset, ok := releaseAsset(runtime.GOOS, runtime.GOARCH)
	if !ok {
		fmt.Fprintf(os.Stderr, "error: no release binary available for %s/%s\n", runtime.GOOS, runtime.GOARCH)
		return 1
	}

	target := selfPath()
	url := releaseBaseURL + "/" + asset

	fmt.Printf("Downloading %s...\n", url)
	data, err := downloadBinary(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	tmp, err := os.CreateTemp(filepath.Dir(target), ".devops-reinstall-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: creating temp file: %v\n", err)
		return 1
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		fmt.Fprintf(os.Stderr, "error: writing temp file: %v\n", err)
		return 1
	}
	if err := tmp.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if err := os.Rename(tmp.Name(), target); err != nil {
		fmt.Fprintf(os.Stderr, "error: replacing %s: %v (do you need to run with sudo?)\n", target, err)
		return 1
	}

	fmt.Printf("Reinstalled devops to %s\n", target)
	return 0
}

// releaseAsset maps a GOOS/GOARCH pair to the release artifact name built by
// .github/workflows/release.yml.
func releaseAsset(goos, goarch string) (string, bool) {
	switch goos + "/" + goarch {
	case "linux/amd64":
		return "devops-linux-amd64", true
	case "darwin/amd64":
		return "devops-darwin-amd64", true
	case "darwin/arm64":
		return "devops-darwin-arm64", true
	default:
		return "", false
	}
}

func downloadBinary(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("downloading release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading release: unexpected status %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading release: %w", err)
	}
	return data, nil
}

//go:build !linux

// Package workload contains workload (container/VM/pod) collectors.
//
// On non-Linux platforms the package is intentionally empty: the Docker
// transport in docker.go talks to /var/run/docker.sock and the npipe
// equivalent is not implemented yet. A sibling docker_windows.go using
// github.com/Microsoft/go-winio will land when the agent supports Windows
// hosts.
package workload

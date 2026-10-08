//go:build !unix

/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import "os/exec"

// processGroupsSupported is false where the agent cannot be contained; the
// launcher then blocks every run instead of leaving descendants uncontrolled.
const processGroupsSupported = false

func setProcessGroup(*exec.Cmd) {}

func killProcessGroup(int) {}

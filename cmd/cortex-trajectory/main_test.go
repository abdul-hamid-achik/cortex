/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

package main

import (
	"strings"
	"testing"
)

func TestRunRejectsMissingCommandsAndExtraArguments(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{nil, "usage"},
		{[]string{"unknown"}, "unknown"},
		{[]string{"validate", "extra", "--manifest", "unused"}, "positional"},
		{[]string{"run", "extra", "--manifest", "unused", "--launcher", "unused"}, "positional"},
	}
	for _, test := range tests {
		if err := run(test.args); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("run(%v) error=%v want=%q", test.args, err, test.want)
		}
	}
}

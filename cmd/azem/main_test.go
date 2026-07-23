package main

import (
	"bytes"
	"testing"
)

func TestPrintVersion(t *testing.T) {
	oldVersion, oldCommit, oldBuildTime := version, gitCommit, buildTime
	version, gitCommit, buildTime = "v1.2.3", "abc1234", "2026-03-20T12:34:56Z"
	t.Cleanup(func() {
		version, gitCommit, buildTime = oldVersion, oldCommit, oldBuildTime
	})

	var output bytes.Buffer
	printVersion(&output)
	const want = "azem v1.2.3\ngit commit: abc1234\nbuild time: 2026-03-20T12:34:56Z\n"
	if output.String() != want {
		t.Fatalf("version output = %q, want %q", output.String(), want)
	}
}

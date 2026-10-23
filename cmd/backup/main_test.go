package main

import "testing"

func TestExitCodeVersion(t *testing.T) {
	if exitCode([]string{"version"}) != 0 {
		t.Fatal("version")
	}
	if exitCode(nil) == 0 {
		t.Fatal("usage")
	}
}

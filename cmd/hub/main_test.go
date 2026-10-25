package main

import (
	"testing"

	"github.com/Rhionin/stockbot/internal/hub"
)

func TestHubCommandFlags(t *testing.T) {
	if hub.Main([]string{"--self-check"}) != 0 {
		t.Fatal("self-check should succeed")
	}
	if hub.Main([]string{"--help"}) != 2 {
		t.Fatal("help should exit 2")
	}
}

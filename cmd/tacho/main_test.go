package main

import (
	"testing"

	"github.com/kosako/tachograph/internal/testhome"
)

// TestMain gives the whole run a throwaway home (#377): see testhome.Run.
func TestMain(m *testing.M) { testhome.Run(m) }

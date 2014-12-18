package main

import (
	"os"
	"testing"
)

func TestLongLines(t *testing.T) {
	w := NewBreakLongLineWriter(os.Stdout, 10)
	w.Write([]byte("0123456789012345678901234567890123456789\n012345678901234567890123456789\n0123456789\n0123"))
}

package cli

import "testing"

func TestIsGoRunBinary(t *testing.T) {
	for exe, want := range map[string]bool{
		"/tmp/go-build3409825126/b001/exe/nanicommit":      true,
		"/var/folders/x/T/go-build123/b001/exe/nanicommit": true,
		"/home/me/go/bin/nanicommit":                       false,
		"/home/me/src/hook/bin/nanicommit":                 false,
		"/home/me/go-builds/exe-files/nanicommit":          false,
	} {
		if got := isGoRunBinary(exe); got != want {
			t.Errorf("isGoRunBinary(%q) = %v, want %v", exe, got, want)
		}
	}
}

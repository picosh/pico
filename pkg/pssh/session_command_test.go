package pssh

import "testing"

func TestSessionCommand(t *testing.T) {
	for command, want := range map[string]string{
		"shell":                 "shell",
		"pub my-topic -b=false": "pub",
		"  ls  ":                "ls",
		"":                      "",
	} {
		if got := sessionCommand(command); got != want {
			t.Errorf("sessionCommand(%q) = %q, want %q", command, got, want)
		}
	}
}

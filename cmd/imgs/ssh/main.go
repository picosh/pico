package main

import "github.com/picosh/pico/prose"

func main() {
	// we are using prose here because we no longer need a dedicated
	// SSH app for imgs since prose handles images just fine
	prose.StartSshServer()
}

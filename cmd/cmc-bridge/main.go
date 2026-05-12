package main

import (
	"os"

	"github.com/parthsareen/codex-mission-control/internal/mission"
)

func main() {
	os.Exit(mission.RunBridge(os.Args[1:], os.Stdout, os.Stderr))
}

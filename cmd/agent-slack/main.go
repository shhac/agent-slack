package main

import (
	"github.com/shhac/agent-slack/internal/cli"
)

var version = "dev"

func main() {
	cli.Run(version)
}

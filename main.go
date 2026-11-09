package main

import (
	"os"

	"github.com/UtakataKyosui/gh-pj-synker/cmd"
)

func main() {
	os.Exit(cmd.Execute(os.Args[1:], os.Stdout, os.Stderr))
}

package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/cli/go-gh/v2/pkg/repository"
	"golang.org/x/term"

	"github.com/UtakataKyosui/gh-pj-synker/internal/cli"
	"github.com/UtakataKyosui/gh-pj-synker/internal/ghapi"
)

// version is set at build time with -ldflags "-X main.version=v1.2.3".
var version = ""

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	width := 0
	if w, _, err := term.GetSize(int(os.Stderr.Fd())); err == nil {
		width = w
	}
	streams := cli.Streams{
		In: os.Stdin, Out: os.Stdout, Err: os.Stderr,
		InTTY:  term.IsTerminal(int(os.Stdin.Fd())),
		OutTTY: term.IsTerminal(int(os.Stdout.Fd())),
		ErrTTY: term.IsTerminal(int(os.Stderr.Fd())),
		Width:  width,
		Getenv: os.Getenv,
	}
	deps := cli.Deps{
		NewAPI: func() (cli.API, error) { return ghapi.New() },
		CurrentRepo: func() (string, error) {
			r, err := repository.Current()
			if err != nil {
				return "", err
			}
			return r.Owner + "/" + r.Name, nil
		},
		Getwd:   os.Getwd,
		Version: resolveVersion(),
	}
	return cli.Run(ctx, os.Args[1:], streams, deps)
}

func resolveVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

package cmd

import (
	"github.com/cli/go-gh/v2/pkg/repository"
)

func currentGitRepo() (string, error) {
	r, err := repository.Current()
	if err != nil {
		return "", err
	}
	return r.Owner + "/" + r.Name, nil
}

// Package ident validates identifiers shared by options and the config file.
package ident

import "regexp"

var (
	loginRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	repoRe  = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9._-]{1,100}$`)
)

// Login reports whether s is a valid GitHub user or organization login.
func Login(s string) bool { return loginRe.MatchString(s) }

// Repo reports whether s is a valid OWNER/REPO.
func Repo(s string) bool { return repoRe.MatchString(s) }

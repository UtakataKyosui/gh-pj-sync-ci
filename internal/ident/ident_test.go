package ident

import "testing"

func TestLogin(t *testing.T) {
	for s, want := range map[string]bool{
		"acme": true, "a": true, "a-b-1": true, "1password": true,
		"": false, "-acme": false, "a b": false, "a/b": false, "a_b": false,
		"abcdefghijabcdefghijabcdefghijabcdefghi": true, "abcdefghijabcdefghijabcdefghijabcdefghij": false,
	} {
		if Login(s) != want {
			t.Errorf("Login(%q) = %v", s, !want)
		}
	}
}

func TestRepo(t *testing.T) {
	for s, want := range map[string]bool{
		"acme/web": true, "acme/web.js": true, "a/b_c-d": true,
		"acme": false, "acme/": false, "/web": false, "a/b/c": false, "a b/c": false, "a/b c": false,
	} {
		if Repo(s) != want {
			t.Errorf("Repo(%q) = %v", s, !want)
		}
	}
}

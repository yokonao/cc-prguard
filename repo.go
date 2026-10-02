package main

import (
	"os/exec"
	"strings"
)

// resolveRepo returns the owner/repo that a PR created in dir targets.
// Like gh, it prefers the `gh repo set-default` remote (gh-resolved=base) and falls back to origin.
func resolveRepo(dir string) string {
	remote := "origin"
	if out, err := exec.Command("git", "-C", dir, "config", "--get-regexp", `^remote\..*\.gh-resolved$`).Output(); err == nil {
		for line := range strings.Lines(string(out)) {
			key, val, _ := strings.Cut(strings.TrimSpace(line), " ")
			if val == "base" {
				remote = strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".gh-resolved")
			}
		}
	}
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", remote).Output()
	if err != nil {
		return ""
	}
	return normalizeRepo(strings.TrimSpace(string(out)))
}

// normalizeRepo converts owner/repo, HOST/owner/repo, and https / ssh URLs to owner/repo.
func normalizeRepo(s string) string {
	s = strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git")
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == ':' })
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1]
}

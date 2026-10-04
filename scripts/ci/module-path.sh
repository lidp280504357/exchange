#!/usr/bin/env bash
# The Go module path is github.com/skill/exchange (go.mod; since 2026-10-04
# it carries no GitHub account). Another github.com/<owner>/exchange in
# code, configuration or docs is a leftover of the old path, and some fail
# silently: a depguard rule that matches nothing, an -X flag that sets
# nothing. Web addresses (https://github.com/...) name the repository and
# do not count.
set -euo pipefail

if git grep -n -I -E '(^|[^/])github\.com/[[:alnum:]_.-]+/exchange([^[:alnum:]_.-]|$)' -- ':!.claude' |
  grep -v 'github\.com/skill/exchange'; then
  echo "::error::the module path is github.com/skill/exchange (go.mod); the lines above name another"
  exit 1
fi

#!/usr/bin/env bash
# CI only. Runs go test -race -json (extra arguments are passed on; default ./...) and
# prints a log that keeps every failure whole: one summary line per package, as plain go
# test prints it, then a delimited block, with every output line and its timestamp, for
# each failed test, each test that started but never finished in a failed package (a
# timeout, or a binary that died mid-test), and each failed package that has output of its
# own (a panic, a build error). Passing tests print nothing, so a green log stays about as
# short as go test's. The filter is go-test-ci.jq next to this script. The JSON stream is
# kept at ${RUNNER_TEMP}/go-test.json. Exits with go test's exit code, or 1 when go test
# passed but the filter failed (a broken formatter must not go unnoticed). If the filter
# fails, the raw JSON is printed instead. Stderr is not redirected, so build errors printed
# outside the JSON still show.
set -euo pipefail

if [ "$#" -eq 0 ]; then
  set -- ./...
fi

here="$(cd "$(dirname "$0")" && pwd)"
json="${RUNNER_TEMP:-/tmp}/go-test.json"
rc=0
go test -race -json "$@" > "$json" || rc=$?

jqrc=0
jq -n -R -r -f "$here/go-test-ci.jq" "$json" || jqrc=$?
if [ "$jqrc" -ne 0 ]; then
  echo "go-test-ci: jq failed (exit $jqrc); raw go test -json output follows" >&2
  cat "$json"
  if [ "$rc" -eq 0 ]; then
    rc=1
  fi
fi

exit "$rc"

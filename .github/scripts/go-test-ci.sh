#!/usr/bin/env bash
# CI only. Runs go test -race -json (extra arguments are passed on; default ./...) and
# prints a log that keeps every failure whole: one summary line per package, as plain go
# test prints it, then for each failed test, and for each failed package that has output
# of its own (a panic, a timeout, a build error), a delimited block with every output line
# and its timestamp. Passing tests print nothing, so a green log stays about as short as
# go test's. The JSON stream is kept at ${RUNNER_TEMP}/go-test.json. Exits with go test's
# exit code. Stderr is not redirected, so build errors printed outside the JSON still show.
set -euo pipefail

if [ "$#" -eq 0 ]; then
  set -- ./...
fi

json="${RUNNER_TEMP:-/tmp}/go-test.json"
rc=0
go test -race -json "$@" > "$json" || rc=$?

# jq -R reads each line as text and parses it; a line that is not JSON is printed as-is.
prog=$(cat <<'EOF'
def pkgof: ((.Package // .ImportPath // "") | sub(" \\[.*\\]$"; ""));
def chunks: ((.Output // "") | sub("\n$"; "") | split("\n"));
def isout: .Action == "output" or .Action == "build-output";
def summary: test("^(ok|FAIL|\\?)[ \\t]");
[inputs | fromjson? // {"Action": "raw", "Output": .}] as $ev
| (
    ($ev[] | select(isout and .Test == null) | chunks[] | select(summary)),
    ($ev[] | select(.Action == "raw") | .Output),
    (
      ($ev | map(select(.Action == "fail" and .Test != null) | [pkgof, .Test]) | unique)[] as [$p, $t]
      | "===== FAIL \($p) \($t) =====",
        ($ev[] | select(isout and .Test == $t and pkgof == $p) | . as $e | chunks[] | "\($e.Time // "-") \(.)")
    ),
    (
      ($ev | map(select((.Action == "fail" or .Action == "build-fail") and .Test == null) | pkgof) | unique)[] as $p
      | [$ev[] | select(isout and .Test == null and pkgof == $p) | . as $e | chunks[] | select(summary | not) | "\($e.Time // "-") \(.)"] as $body
      | select($body | length > 0)
      | "===== FAIL \($p) (package output) =====", $body[]
    )
  )
EOF
)
jq -n -R -r "$prog" "$json"

exit "$rc"

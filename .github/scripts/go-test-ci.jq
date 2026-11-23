# Used by go-test-ci.sh: jq -n -R -r -f go-test-ci.jq <go-test.json>
# Each input line is text; a line that is not JSON is printed as-is.
def pkgof: ((.Package // .ImportPath // "") | sub(" \\[.*\\]$"; ""));
def chunks: ((.Output // "") | sub("\n$"; "") | split("\n"));
def isout: .Action == "output" or .Action == "build-output";
def summary: test("^(ok|FAIL|\\?)[ \\t]");
def key($p; $t): "\($p)\t\($t)";
def block($ev; $p; $t): ($ev[] | select(isout and .Test == $t and pkgof == $p) | . as $e | chunks[] | "\($e.Time // "-") \(.)");
[inputs | fromjson? // {"Action": "raw", "Output": .}] as $ev
| ($ev | map(select(.Test != null and (.Action == "pass" or .Action == "fail" or .Action == "skip")) | {key: key(pkgof; .Test), value: true}) | from_entries) as $done
| ($ev | map(select(.Action == "fail" and .Test == null) | pkgof) | unique) as $failedpkgs
| (
    ($ev[] | select(isout and .Test == null) | chunks[] | select(summary)),
    ($ev[] | select(.Action == "raw") | .Output),
    (
      ($ev | map(select(.Action == "fail" and .Test != null) | [pkgof, .Test]) | unique)[] as [$p, $t]
      | "===== FAIL \($p) \($t) =====",
        block($ev; $p; $t)
    ),
    # A timeout, or a binary that dies mid-test, never gives the running test a terminal action.
    (
      ($ev | map(select(.Action == "run" and .Test != null) | [pkgof, .Test]) | unique)[] as [$p, $t]
      | select(IN($p; $failedpkgs[]) and ($done[key($p; $t)] | not))
      | "===== FAIL \($p) \($t) (did not finish) =====",
        block($ev; $p; $t)
    ),
    (
      ($ev | map(select((.Action == "fail" or .Action == "build-fail") and .Test == null) | pkgof) | unique)[] as $p
      | [$ev[] | select(isout and .Test == null and pkgof == $p) | . as $e | chunks[] | select(summary | not) | "\($e.Time // "-") \(.)"] as $body
      | select($body | length > 0)
      | "===== FAIL \($p) (package output) =====", $body[]
    )
  )

# 020: API-equivalent cost

Covers what the automated tests cannot see for `specs/020-api-equivalent-cost` (AC12): the
cost in a real browser, on the real gateway running the build, light and dark, at 1,440 px
and 2,560 px. The automated parts are `internal/cost/cost_test.go`,
`internal/protocols/anthropic/cost_test.go`, `internal/timeline/cost_test.go`,
`internal/ui/cost_test.go`, `internal/ui/webtest/cost.test.js`, and the cost tests in
`call-dialog.test.js` and `sessions-page.test.js`. Record expected against seen in the
spec's research.md.

## Setup

1. Real gateway: build this branch and run it as the service (keep a copy of the binary
   it replaces):

   ```sh
   make build
   cp ~/.local/bin/gateway ~/.local/bin/gateway.bak-020
   install -m 0755 bin/gateway ~/.local/bin/gateway
   systemctl --user restart llm-gateway
   ```

2. Throwaway gateway for a model with no price (the real store has none, research Notes),
   on its own ports and a fresh data folder, forwarding to the seed's stub:

   ```sh
   GATEWAY_LISTEN_ADDR=127.0.0.1:17297 GATEWAY_UI_LISTEN_ADDR=127.0.0.1:17299 \
   GATEWAY_CAPTURE_DIR=/tmp/fx020-data GATEWAY_EFFECTS_ENABLED=false \
   GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL=http://127.0.0.1:17296 \
   bin/gateway -config <copy of your config>   # in the background
   node docs/manual-checks/seed-cost.mjs        # prints the session's link
   ```

   It seeds one session: a call priced with the 5m/1h split ($0.144), one with no split
   ($0.074, estimated), one on `claude-seed-no-price`, and a 429 (no usage).

3. Chrome: each step at 1,440 px and at 2,560 px wide (confirm the width in each
   screenshot: `window.innerWidth`), in the light and the dark theme.

## §1 Sessions list (real gateway, http://127.0.0.1:7199/)

| Step | Expected |
|---|---|
| 1.1 | The list has a column "API-eq. cost" after Tokens; each row shows a dollar amount to cents on one line. Hovering the header says "API-equivalent: what these tokens would cost at the provider's published API prices — not what was charged." |
| 1.2 | Hovering a row's amount shows the same explanation, with Anthropic named; a row whose calls include a cut call (no usage counts) reads "≥ $…" and its hover says how many calls were left out. |
| 1.3 | The Session column keeps at least 20rem at 1,440 px and at 2,560 px (computed width of `td.title` ≥ 320 px). |
| 1.4 | The Today line ends with "$X API-eq. cost"; its hover explains it. |

## §2 A real session (normal, and one with side calls)

| Step | Expected |
|---|---|
| 2.1 | The overview's foot reads "API-equivalent cost: $X" after the tokens line; the hover explains it and says side calls are included; X matches the session's row in the list. |
| 2.2 | Opening a main call: the dialog's top shows "API-eq. cost $…"; the "API-equivalent cost" section lists Input, Cache write 5m, Cache write 1h, Cache read and Output, each tokens × $/MTok = amount, then the total. |
| 2.3 | Opening a side call (title generation or quota probe): it shows its own cost the same way. |
| 2.4 | Arithmetic for one real session: the sum of tokens by kind × the published price equals the cost shown (computed from the API's numbers, by hand). |

## §3 A session with a model that has no price (throwaway, http://127.0.0.1:17299/)

| Step | Expected |
|---|---|
| 3.1 | The seeded row reads "≥ $0.22"; its hover: "1 call left out: no price for claude-seed-no-price", "1 call estimated …", "1 call failed with no usage and cost nothing". |
| 3.2 | The no-price call's dialog: "— no price for claude-seed-no-price" in the top and in the cost section. |
| 3.3 | The no-split call's dialog: "$0.07 (estimated)", every cache write on the 5m line. |
| 3.4 | The 429 call's dialog: cost "—", the hover saying no usage, nothing billed. |
| 3.5 | The overview foot: "API-equivalent cost: ≥ $0.22". |

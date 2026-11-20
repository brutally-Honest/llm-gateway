# >>> llm-gateway >>>
# Managed by gateway install; `gateway uninstall` removes it. Edits here are overwritten.
claude() {
  if curl -fsS --max-time 1 http://127.0.0.1:7197/healthz >/dev/null 2>&1; then
    command claude --settings "$HOME/.config/llm-gateway/claude-settings.json" "$@"
  else
    echo "llm-gateway is not running: starting plain Claude Code" >&2
    command claude "$@"
  fi
}
# <<< llm-gateway <<<

# 047 research

## Progress
- 2026-11-26 spec and research created on fix/047-flake-and-banner

## Decisions
- **D1** 2026-11-26 Dismissal stores `{since, lost}` from the answer shown when dismissed
  (sessionStorage, in-memory fallback). The banner stays hidden while `since` is the same and
  `lost` is not greater; it shows again when `lost` rises or `since` differs (a restart: its
  count restarts at 0, so any loss after it is new even if lower than the dismissed count).
  Keying on `lost` alone would hide a restart's first losses, breaking "never hide a new loss".
  `since` is already in /api/health (process start), so no server change.
- **D2** 2026-11-26 `docs/observability.md` says the banner "cannot be dismissed"; it is
  updated with U11. That is docs, not a change to /healthz, /api/health, metrics or doctor.

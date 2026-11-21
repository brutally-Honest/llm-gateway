---
status: approved
date: 2026-11-21
---

# 0020 — Geist fonts ship inside the web UI

## Context
Spec 027 adds two UI styles, Lean and Stark, chosen in Settings. Stark, drawn from the
Clarity report style, depends on the Geist and Geist Mono fonts. Clarity loads them from
Google Fonts, but the
UI's security policy allows fonts only from the gateway itself (`default-src 'self'`).
The gateway is a local tool, so it should not call a third party when a page loads, and
it should work offline.

## Decision
The two variable fonts, `Geist-Variable.woff2` and `GeistMono-Variable.woff2` (Vercel's
`geist` npm package 1.9.2, about 70 KB each), are copied into `internal/ui/web/fonts/`
and embedded with the rest of the UI. `style.css` declares them with `@font-face` and a
relative URL. They are used only when the Stark style is on; Lean, the default, keeps
its own font stack. They are licensed under the SIL Open Font License 1.1, which allows
bundling. The licence is in `docs/licenses/geist-OFL.txt`, outside the served folder,
because the web guard (`TestWeb_NoInlineOrExternalReferences`) allows no URLs in served
files.

| File | sha256 |
|---|---|
| Geist-Variable.woff2 | `3626eba547294501c8a4d662c7d9ea3b49734409a4c5a1ccf12fa86429b1687e` |
| GeistMono-Variable.woff2 | `5d93ee62680bbec1abc5405c2658311f9396f6f9a821ef8551ec463887e600d4` |

## Alternatives
- **Load from Google Fonts.** This needs no files in the repo. But it means a call to a
  third party on every page load, a looser security policy, and no fonts offline.
- **System fonts only.** This needs nothing new. But the result only looks close to
  Stark, not the same.

## Consequences
- The binary grows by about 140 KB.
- There is no package manager for these files. Updating them means copying new files
  from a newer `geist` release and updating the checksums here.
- No code dependency is added. Nothing runs from the package; only the two font files
  are copied.

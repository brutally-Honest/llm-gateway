#!/usr/bin/env python3
"""Inline src/core.css and src/core.js into each src page, so every mockup is one
self-contained file. Run from anywhere: python3 specs/016-web-ui-v2/mockups/build.py"""
from pathlib import Path

here = Path(__file__).resolve().parent
src = here / "src"
css = (src / "core.css").read_text()
js = (src / "core.js").read_text()
pages = {
    "v4-story.html": "v4-story.html",
    "sessions.html": "sessions.html",
}
for out, name in pages.items():
    p = src / name
    if not p.exists():
        continue
    html = p.read_text().replace("/*CORE_CSS*/", css).replace("/*CORE_JS*/", js)
    (here / out).write_text(html)
    print("wrote", out)

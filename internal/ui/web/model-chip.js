import { counterText } from "./entries.js";

// ModelChip renders data = a model_call entry: model, status, duration and the four counters.
export class ModelChip extends HTMLElement {
  render(e) {
    this.className = "model-chip";
    const u = e.usage || {};
    const parts = [
      e.model || "unknown model",
      e.status === undefined || e.status === null ? "status unknown" : `status ${e.status}`,
      e.duration_ms === undefined || e.duration_ms === null ? "duration unknown" : `${e.duration_ms} ms`,
      `in ${counterText(u.input)}`,
      `out ${counterText(u.output)}`,
      `cache write ${counterText(u.cache_write)}`,
      `cache read ${counterText(u.cache_read)}`,
    ];
    if (u.partial || e.partial) parts.push("partial");
    this.textContent = parts.join(" · ");
    if (e.status >= 400) this.classList.add("is-error");
  }
}

customElements.define("model-chip", ModelChip);

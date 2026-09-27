// health-status — the topbar readiness pill. It checks /readyz once per
// page. It is deliberately not a live region: a status announced on every
// page load is noise; the label is there when a screen reader looks for it.
//
// Parts: label.
export default function healthStatus(root, { part }) {
  const label = part("label");
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 5000);

  const show = (state, text) => {
    root.dataset.state = state;
    root.title = text;
    if (label) {
      label.textContent = text;
    }
  };

  fetch("/readyz", { signal: controller.signal, cache: "no-store", headers: { Accept: "application/json" } })
    .then((response) => (response.ok ? response.json() : Promise.reject(new Error(`status ${response.status}`))))
    .then((body) => {
      if (body?.status !== "ready") {
        throw new Error("not ready");
      }
      show("ready", "Registry ready");
    })
    .catch(() => show("error", "Registry unavailable"))
    .finally(() => clearTimeout(timeout));

  return () => {
    clearTimeout(timeout);
    controller.abort();
  };
}

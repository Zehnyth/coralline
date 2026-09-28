// sidebar-trigger — the topbar button that opens the mobile navigation
// drawer. It only talks to the sidebar through events.
import { combine, emit, listen, on } from "../core/dom.js";

export default function sidebarTrigger(root) {
  return combine(
    on(root, "click", () => emit("sidebar:toggle", { opener: root })),
    listen("sidebar:state", ({ open }) => root.setAttribute("aria-expanded", String(Boolean(open)))),
  );
}

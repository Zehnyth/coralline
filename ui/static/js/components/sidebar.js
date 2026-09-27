// sidebar — the app shell's navigation panel (templates/shell/sidebar.html).
//
// Desktop (>700px): the toggle next to the wordmark collapses the panel into
// an icon rail and back. Expanded is the default; a collapsed choice is
// remembered and re-applied before first paint by the head script
// (:root[data-sidebar="collapsed"]).
// Mobile (<=700px): an off-canvas drawer opened by sidebar-trigger. While
// open, the page behind it is inert (so focus stays in the drawer), focus
// moves to the current nav item, and Escape, the backdrop, or the toggle
// (which becomes a close button) return focus to whatever opened it.
//
// Parts: panel, backdrop, toggle. Props: content (id of the element made
// inert while the drawer is open). Events: listens for sidebar:toggle,
// emits sidebar:state.
import { combine, emit, listen, on } from "../core/dom.js";
import { useMediaQuery, useStorage } from "../core/hooks.js";

const MOBILE = "(max-width: 700px)";

export default function sidebar(root, { part, props }) {
  const panel = part("panel");
  const backdrop = part("backdrop");
  const toggle = part("toggle");
  if (!panel || !backdrop || !toggle) {
    return undefined;
  }
  const html = document.documentElement;
  const content = props.content ? document.getElementById(props.content) : null;
  const stored = useStorage("coralline-sidebar");
  let opener = null;

  const isOpen = () => panel.classList.contains("is-open");
  const isCollapsed = () => html.dataset.sidebar === "collapsed";

  function label(text, expanded) {
    toggle.setAttribute("aria-label", text);
    toggle.title = text;
    toggle.setAttribute("aria-expanded", String(expanded));
  }

  function syncToggle(mobile) {
    if (mobile) {
      label("Close navigation", isOpen());
    } else {
      label(isCollapsed() ? "Expand sidebar" : "Collapse sidebar", !isCollapsed());
    }
  }

  function setOpen(open, restoreFocus = true) {
    panel.classList.toggle("is-open", open);
    backdrop.hidden = !open;
    if (content) {
      content.inert = open;
    }
    syncToggle(true);
    emit("sidebar:state", { open });
    if (open) {
      (panel.querySelector('.nav-item[aria-current="page"]') ?? panel.querySelector(".nav-item"))?.focus();
    } else {
      if (restoreFocus) {
        opener?.focus();
      }
      opener = null;
    }
  }

  function setCollapsed(collapsed) {
    if (collapsed) {
      html.dataset.sidebar = "collapsed";
      stored.set("collapsed");
    } else {
      delete html.dataset.sidebar;
      stored.remove();
    }
    syncToggle(false);
  }

  const media = useMediaQuery(MOBILE, (mobile) => {
    // Leaving the mobile layout closes the drawer without stealing focus.
    if (!mobile && isOpen()) {
      setOpen(false, false);
    }
    syncToggle(mobile);
  });
  syncToggle(media.matches());

  return combine(
    media.off,
    on(toggle, "click", () => (media.matches() ? setOpen(false) : setCollapsed(!isCollapsed()))),
    on(backdrop, "click", () => setOpen(false)),
    on(document, "keydown", (event) => {
      if (event.key === "Escape" && isOpen()) {
        setOpen(false);
      }
    }),
    listen("sidebar:toggle", ({ opener: trigger }) => {
      if (isOpen()) {
        setOpen(false);
      } else {
        opener = trigger ?? null;
        setOpen(true);
      }
    }),
  );
}

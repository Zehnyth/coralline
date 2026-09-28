// theme-toggle — switches between the light and dark themes.
//
// With no saved choice the page follows the OS colour scheme (CSS
// light-dark() tokens do the work) and this button follows it too. Clicking
// pins the opposite of what is showing now, in :root[data-theme] and in
// storage; the head script re-applies it before first paint on later pages.
// The button is a toggle for "Dark theme": aria-pressed says whether it's on.
import { combine, on } from "../core/dom.js";
import { useMediaQuery, useStorage } from "../core/hooks.js";

export default function themeToggle(root) {
  const html = document.documentElement;
  const stored = useStorage("coralline-theme");
  const prefersDark = useMediaQuery("(prefers-color-scheme: dark)", () => {
    if (!html.dataset.theme) {
      sync();
    }
  });

  function current() {
    if (html.dataset.theme === "light" || html.dataset.theme === "dark") {
      return html.dataset.theme;
    }
    return prefersDark.matches() ? "dark" : "light";
  }

  function sync() {
    const dark = current() === "dark";
    root.setAttribute("aria-pressed", String(dark));
    root.title = dark ? "Switch to light theme" : "Switch to dark theme";
  }

  sync();
  return combine(
    prefersDark.off,
    on(root, "click", () => {
      const next = current() === "dark" ? "light" : "dark";
      html.dataset.theme = next;
      stored.set(next);
      sync();
    }),
  );
}

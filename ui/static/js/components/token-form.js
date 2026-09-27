// token-form — the create-token form. The custom permission checkboxes only
// apply when Access is "Custom", and the hours field only when Expires is
// "Custom", so each is hidden and disabled otherwise (a disabled fieldset's
// inputs aren't submitted). Without JavaScript both stay visible, and the
// server rejects custom permissions sent with another role instead of
// ignoring them.
//
// Parts: role (the Access radios), role-hint, role-descriptions (a
// <template> of data-role spans), custom-permissions (fieldset), expiry
// (the Expires radios), custom-expiry.
import { combine, on } from "../core/dom.js";

export default function tokenForm(root, { part, parts }) {
  const roles = parts("role");
  const custom = part("custom-permissions");
  const hint = part("role-hint");
  const descriptions = part("role-descriptions");
  const expiries = parts("expiry");
  const customExpiry = part("custom-expiry");
  const hours = customExpiry?.querySelector("input");

  const checked = (radios) => radios.find((radio) => radio.checked)?.value;

  function syncRole() {
    const role = checked(roles);
    if (custom) {
      custom.disabled = custom.hidden = role !== "custom";
    }
    const text = descriptions?.content.querySelector(`[data-role="${role}"]`)?.textContent;
    if (hint && text) {
      hint.textContent = text;
    }
  }

  function syncExpiry() {
    if (!customExpiry) {
      return;
    }
    const isCustom = checked(expiries) === "custom";
    customExpiry.hidden = !isCustom;
    if (hours) {
      hours.disabled = !isCustom;
      hours.required = isCustom;
    }
  }

  syncRole();
  syncExpiry();
  return combine(...roles.map((role) => on(role, "change", syncRole)), ...expiries.map((radio) => on(radio, "change", syncExpiry)));
}

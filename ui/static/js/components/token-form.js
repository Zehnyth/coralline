// token-form — the create-token form. The custom permission checkboxes only
// apply when Access is "Custom", so they are disabled otherwise (a disabled
// fieldset's inputs aren't submitted). Without JavaScript the server rejects
// custom permissions sent with another role instead of ignoring them.
//
// Parts: role (the Access radios), custom-permissions (the fieldset).
import { combine, on } from "../core/dom.js";

export default function tokenForm(root, { part, parts }) {
  const custom = part("custom-permissions");
  const roles = parts("role");
  if (!custom || roles.length === 0) {
    return undefined;
  }
  const sync = () => {
    custom.disabled = roles.find((role) => role.checked)?.value !== "custom";
  };
  sync();
  return combine(...roles.map((role) => on(role, "change", sync)));
}

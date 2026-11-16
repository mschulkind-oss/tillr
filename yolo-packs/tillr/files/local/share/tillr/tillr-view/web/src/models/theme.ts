// The colour theme follows the platform: one `prefers-color-scheme` query decides, and the
// stylesheet's dark tokens key off `[data-theme="dark"]` on `<html>`.

export type ColorTheme = "light" | "dark";

const DARK_QUERY = "(prefers-color-scheme: dark)";

/** currentColorTheme is the platform's answer, not a stored preference: this screen has no
 * theme control of its own to disagree with the operating system. */
export function currentColorTheme(): ColorTheme {
  return window.matchMedia(DARK_QUERY).matches ? "dark" : "light";
}

/** applyColorTheme writes the theme to the one hook the stylesheet has. */
export function applyColorTheme(theme: ColorTheme = currentColorTheme()): void {
  document.documentElement.dataset.theme = theme;
}

/** watchColorTheme follows the platform for the life of the document and returns the
 * unsubscribe. */
export function watchColorTheme(): () => void {
  const media = window.matchMedia(DARK_QUERY);
  const onChange = () => applyColorTheme(media.matches ? "dark" : "light");
  media.addEventListener("change", onChange);
  return () => media.removeEventListener("change", onChange);
}

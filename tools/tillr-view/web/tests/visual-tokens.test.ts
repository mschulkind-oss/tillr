import css from "../src/tokens.css?raw";
import { expect, test } from "vitest";

const blocks = [...css.matchAll(/:root(?:\[data-theme="dark"\])?\s*\{([^}]+)\}/g)].map(match => Object.fromEntries([...match[1].matchAll(/(--[\w-]+):\s*([^;]+);/g)].map(token => [token[1], token[2].trim()])));
function luminance(hex: string): number {
  const linear = [1, 3, 5].map(offset => parseInt(hex.slice(offset, offset + 2), 16) / 255).map(value => value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4);
  return linear[0] * 0.2126 + linear[1] * 0.7152 + linear[2] * 0.0722;
}
function contrast(foreground: string, background: string): number {
  const values = [luminance(foreground), luminance(background)].sort((a, b) => b - a);
  return (values[0] + 0.05) / (values[1] + 0.05);
}

test("both themes define all color roles", () => {
  expect(blocks).toHaveLength(2);
  for (const theme of blocks) for (const role of ["--bg", "--surface", "--surface-2", "--line", "--ink-1", "--ink-2", "--ink-3", "--accent", "--state-moving", "--state-ready", "--state-owner", "--state-work", "--state-cause", "--state-pass"]) expect(theme[role], role).toMatch(/^#[\da-f]{6}$/i);
});
for (const [index, theme] of blocks.entries()) {
  for (const background of ["--bg", "--surface", "--surface-2"]) {
    for (const foreground of ["--ink-1", "--ink-2", "--ink-3", "--accent", "--state-moving", "--state-ready", "--state-owner", "--state-work", "--state-cause", "--state-pass"]) {
      test(`${index ? "dark" : "light"} ${foreground} on ${background}: text ≥4.5 and glyph ≥3`, () => {
        const ratio = contrast(theme[foreground], theme[background]);
        expect(ratio).toBeGreaterThanOrEqual(4.5);
        expect(ratio).toBeGreaterThanOrEqual(3);
      });
    }
  }
}
test("the shell owns a bounded scroll viewport so its sticky loop band stays visible", () => {
  const shell = css.match(/\.observatory-shell\s*\{([^}]+)\}/)?.[1];
  expect(shell).toContain("height: 100dvh");
  expect(shell).toContain("overflow: auto");
  expect(css.match(/\.observatory-shell__sticky\s*\{([^}]+)\}/)?.[1]).toContain("position: sticky; top: 0");
});

test("type, spacing and radius tokens match section 6", () => {
  const theme = blocks[0];
  expect([1, 2, 3, 4, 5, 6].map(i => theme[`--s${i}`])).toEqual(["4px", "8px", "12px", "16px", "24px", "32px"]);
  expect(theme["--font-sans"]).toBe('ui-sans-serif, system-ui, "Segoe UI", Roboto, sans-serif');
  expect(theme["--font-mono"]).toBe('ui-monospace, SFMono-Regular, Menlo, "DejaVu Sans Mono", monospace');
  expect(theme["--t-label"]).toBe("11px / 1.2 var(--font-sans)");
  expect(theme["--t-data"]).toBe("12px / 1.3 var(--font-mono)");
  expect(theme["--t-body"]).toBe("13px / 1.45 var(--font-sans)");
  expect(theme["--t-title"]).toBe("600 16px / 1.3 var(--font-sans)");
  expect(theme["--t-page"]).toBe("600 20px / 1.2 var(--font-sans)");
  expect([theme["--r1"], theme["--r2"]]).toEqual(["2px", "4px"]);
});

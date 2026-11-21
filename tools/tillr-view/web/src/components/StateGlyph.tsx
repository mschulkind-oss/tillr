// The third encoding of an item state (design §7.2, ledger `D-OBS8`): colour, word and
// glyph. The glyph is the shape that survives a greyscale print, a colour-blind reader and
// a low-contrast projector, so it is drawn as geometry whose only paint is `currentColor`:
// take the colour away and the shape is still there, because the shape was never the
// colour. One component renders every surface's glyph — a shape copied into each surface is
// a shape that drifts, and two surfaces disagreeing about what `blocked` looks like is
// exactly the defect the third encoding exists to prevent.

import type { ReactNode } from "react";

/** The written table, which is also the design's: the five shapes §7.2 names by hand (a dot
 * for in queue, a filled triangle for working, a bar for blocked, a check for landed, a
 * slash for closed), a shape for each of the two states it leaves unnamed, and `unknown`
 * for a state the projection grows later. The seven shapes are distinct from each other,
 * which is the property the capture-time check pins. */
export const STATE_GLYPHS = {
  not_started: "ring",
  in_queue: "dot",
  working: "triangle",
  blocked: "bar",
  landed: "check",
  resolved_elsewhere: "arrow",
  closed: "slash",
  unknown: "square",
} as const;

/** Redesign classes have their own vocabulary; legacy projection shapes stay intact. */
export const CLASS_GLYPHS = {
  moving: "triangle",
  ready: "dot",
  "needs you": "diamond",
  "waits on work": "bars",
  "needs a cause": "cross",
  alarm: "cross",
  pass: "check",
  skipped: "slash",
  "waits on the loop": "ring",
  stopped: "filled-square",
  "not known": "dashed-ring",
} as const;

/** The seven states the projection is closed over (design §3.1, `serveread.go`
 * `ProjectItemState`). A glyph table missing one of these is the finding §7.2 describes. */
export const PROJECTED_STATES = [
  "not_started",
  "in_queue",
  "working",
  "blocked",
  "landed",
  "resolved_elsewhere",
  "closed",
] as const;

export type StateGlyphName = (typeof STATE_GLYPHS)[keyof typeof STATE_GLYPHS] | (typeof CLASS_GLYPHS)[keyof typeof CLASS_GLYPHS];

/** glyphFor is the fallback rule: a state this build has never seen renders as the unknown
 * shape rather than as no shape, so an eighth state is a visible review finding and not a
 * row whose only mark is its hue. */
export function glyphFor(state: string): StateGlyphName {
  const glyph = ({ ...STATE_GLYPHS, ...CLASS_GLYPHS } as Record<string, StateGlyphName>)[state];
  return glyph ?? STATE_GLYPHS.unknown;
}

/** The shapes, at a 12×12 viewBox and 12 px square. Stroke widths are the geometry's own,
 * never a colour: `currentColor` is the only paint any of them names. */
const SHAPES: Record<StateGlyphName, ReactNode> = {
  diamond: <path d="M6 1 11 6 6 11 1 6Z" fill="currentColor" />,
  bars: <path d="M3 1v10M9 1v10" stroke="currentColor" strokeWidth="2" />,
  cross: <path d="m2 2 8 8M10 2l-8 8" stroke="currentColor" strokeWidth="2" />,
  "dashed-ring": <circle cx="6" cy="6" r="4" fill="none" stroke="currentColor" strokeWidth="1.6" strokeDasharray="2 2" />,
  "filled-square": <rect x="2" y="2" width="8" height="8" fill="currentColor" />,
  ring: <circle cx="6" cy="6" r="4" fill="none" stroke="currentColor" strokeWidth="1.6" />,
  dot: <circle cx="6" cy="6" r="3.6" fill="currentColor" />,
  triangle: <path d="M6 2.1 10.5 9.5H1.5Z" fill="currentColor" />,
  bar: <rect x="1.5" y="4.6" width="9" height="2.8" rx="0.5" fill="currentColor" />,
  check: (
    <path
      d="M2.1 6.4 4.9 9.2 9.9 3.2"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
  ),
  arrow: (
    <path
      d="M1.6 6h7.6M6.3 2.7 9.6 6l-3.3 3.3"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
  ),
  slash: (
    <path
      d="M2.6 9.4 9.4 2.6"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
    />
  ),
  square: (
    <rect
      x="2.4"
      y="2.4"
      width="7.2"
      height="7.2"
      rx="1"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
    />
  ),
};

/** StateGlyph is the one renderer of the third encoding. It is decorative: the state's word
 * is always beside it (design §7.2's word channel), so the glyph is hidden from assistive
 * technology and never becomes the row's accessible name.
 *
 * `data-glyph` names the shape and `data-state` the state it was asked for, which is what
 * lets the capture-time check compare a surface's glyph against the written table without
 * reading a screenshot. */
export function StateGlyph({ state, className }: { state: string; className?: string }) {
  const glyph = glyphFor(state);
  return (
    <svg
      className={className === undefined ? "state-glyph" : `state-glyph ${className}`}
      viewBox="0 0 12 12"
      width="12"
      height="12"
      aria-hidden="true"
      focusable="false"
      data-glyph={glyph}
      data-state={state}
    >
      {SHAPES[glyph]}
    </svg>
  );
}

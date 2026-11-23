// The React side of models/perf.ts: when a page view begins, when its data first reaches the
// screen and when its reads settle, and how long a screen takes to render.

import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useLayoutEffect, useRef, type ReactNode } from "react";

import { perf, startPerf, type PerfRecorder } from "./perf";

const afterPaint = (fn: () => void) => {
  if (typeof requestAnimationFrame === "function") requestAnimationFrame(() => fn());
  else fn();
};

/** Call it first in the root component, so its effect runs before the queries' own: the page
 * view has begun before any read it times has started. */
export function usePagePerf(hash: string, recorder: PerfRecorder = perf): void {
  const client = useQueryClient();
  const first = useRef(true);
  useEffect(() => {
    recorder.beginPage(hash, first.current ? "load" : "route");
    first.current = false;
  }, [hash, recorder]);
  useEffect(() => (recorder === perf ? startPerf(recorder) : undefined), [recorder]);
  useEffect(() => client.getQueryCache().subscribe(event => {
    if (event.type !== "updated") return;
    const kind = event.action.type;
    if (kind !== "success" && kind !== "error") return;
    // The frame after the data arrived is when it is on screen, not the moment it parsed.
    if (kind === "success") afterPaint(() => recorder.firstData());
    if (client.isFetching() === 0) afterPaint(() => recorder.settled());
  }), [client, recorder]);
}

/** Times its children's render and commit: the body runs before theirs and the layout
 * effect after theirs, so the difference is the subtree's. Only slow ones are reported. */
export function RenderTimer({ id, children, recorder = perf }: { id: string; children: ReactNode; recorder?: PerfRecorder }) {
  const started = performance.now();
  useLayoutEffect(() => {
    recorder.rendered(id, performance.now() - started);
  });
  return <>{children}</>;
}

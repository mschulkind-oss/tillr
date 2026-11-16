// The viewer's clock as React state. Two surfaces need it and for the same reason: a live
// elapsed that never moves is a timestamp, not an elapsed, and a paused screen's age is
// only honest if it keeps counting. A test pins the clock by handing the component its own
// `now`; the hook is the board's default.

import { useEffect, useState } from "react";

/** `now` ticking every `intervalMs`, so a duration drawn from it ages on screen. */
export function useNow(intervalMs = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}

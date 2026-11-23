// Path routing over the History API. The route is `location.pathname + location.search`
// (`/runs`, `/admin/runs?after=5`); there is no router library, and a link is a real
// `<a href>` so middle-click and open-in-new-tab work. A plain left click is intercepted
// by `useLinkInterception` and becomes a `pushState`, so navigating does not reload.

import { useEffect, useState } from "react";

/** History API writes do not emit native popstate. Notify every route reader after a
 * navigation, so the shell and every screen see the same URL. */
export const URL_CHANGE_EVENT = "observatory:urlchange";

export function notifyURLChange(): void {
  queueMicrotask(() => window.dispatchEvent(new Event(URL_CHANGE_EVENT)));
}

/** currentRoute is the path and query the screens read; the fragment is never a route. */
export function currentRoute(): string {
  return `${window.location.pathname}${window.location.search}`;
}

/** navigate moves to a route without reloading. */
export function navigate(to: string, options?: { replace?: boolean }): void {
  if (to === currentRoute()) return;
  if (options?.replace) window.history.replaceState(null, "", to);
  else window.history.pushState(null, "", to);
  window.dispatchEvent(new Event(URL_CHANGE_EVENT));
}

/** useScreenRoute follows the location: path changes, back and forward. */
export function useScreenRoute(): string {
  const [route, setRoute] = useState(currentRoute);
  useEffect(() => {
    const changed = () => setRoute(currentRoute());
    changed();
    window.addEventListener("popstate", changed);
    window.addEventListener(URL_CHANGE_EVENT, changed);
    return () => {
      window.removeEventListener("popstate", changed);
      window.removeEventListener(URL_CHANGE_EVENT, changed);
    };
  }, []);
  return route;
}

/** useLinkInterception turns a plain click on a same-origin `<a href>` into a history
 * navigation. A modified click, a non-left button, a `target`, a `download`, an external
 * origin and a bare in-page fragment are left to the browser. */
export function useLinkInterception(): void {
  useEffect(() => {
    const onClick = (event: MouseEvent) => {
      if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
      const anchor = event.target instanceof Element ? event.target.closest("a[href]") : null;
      if (!(anchor instanceof HTMLAnchorElement)) return;
      if ((anchor.target && anchor.target !== "_self") || anchor.hasAttribute("download")) return;
      const url = new URL(anchor.href, window.location.href);
      if (url.origin !== window.location.origin) return;
      if (url.pathname.startsWith("/api/")) return;
      if (url.pathname === window.location.pathname && url.search === window.location.search && url.hash) return;
      event.preventDefault();
      navigate(`${url.pathname}${url.search}${url.hash}`);
    };
    document.addEventListener("click", onClick);
    return () => document.removeEventListener("click", onClick);
  }, []);
}

import { cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";
import { currentRoute, navigate, useLinkInterception, useScreenRoute } from "../src/models/route";

afterEach(() => { cleanup(); vi.restoreAllMocks(); window.history.replaceState(null, "", "/"); });

describe("navigation", () => {
  test("navigate pushes a path and notifies readers", async () => {
    const { result } = renderHook(() => useScreenRoute());
    navigate("/admin/runs?after=3");
    expect(window.location.pathname).toBe("/admin/runs");
    expect(window.location.search).toBe("?after=3");
    return waitFor(() => expect(result.current).toBe("/admin/runs?after=3"));
  });

  test("a plain click on an internal link navigates in place; modified clicks and external links are left alone", () => {
    renderHook(() => useLinkInterception());
    const host = document.createElement("div");
    host.innerHTML = '<a id="in" href="/runs?x=1">runs</a><a id="ext" href="https://example.test/runs">ext</a><a id="blank" href="/admin" target="_blank">b</a><a id="api" href="/api/v1/snapshot">api</a>';
    document.body.append(host);
    const push = vi.spyOn(window.history, "pushState");
    const outcome = (id: string, init: MouseEventInit = {}) => {
      const event = new MouseEvent("click", { bubbles: true, cancelable: true, button: 0, ...init });
      host.querySelector(`#${id}`)!.dispatchEvent(event);
      return event.defaultPrevented;
    };
    expect(outcome("in")).toBe(true);
    expect(push).toHaveBeenCalledTimes(1);
    expect(currentRoute()).toBe("/runs?x=1");
    expect(outcome("in", { ctrlKey: true })).toBe(false);
    expect(outcome("in", { metaKey: true })).toBe(false);
    expect(outcome("in", { button: 1 })).toBe(false);
    expect(outcome("ext")).toBe(false);
    expect(outcome("blank")).toBe(false);
    expect(outcome("api")).toBe(false);
    expect(push).toHaveBeenCalledTimes(1);
    host.remove();
  });
});

import { cleanup, fireEvent, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";
import { currentRoute, legacyHashTarget, migrateLegacyHash, navigate, useLinkInterception, useScreenRoute } from "../src/models/route";
import { canonicalRoute } from "../src/App";

afterEach(() => { cleanup(); vi.restoreAllMocks(); window.history.replaceState(null, "", "/"); });

describe("legacy hash routes", () => {
  test.each([
    ["#/runs", "", "/runs"],
    ["#/admin", "", "/admin"],
    ["#/", "", "/"],
    ["#/items?state=unlanded&class=needs+you", "", "/items?state=unlanded&class=needs+you"],
    ["#/item/item-a?run=r1", "", "/item/item-a?run=r1"],
    ["#/admin/runs/12", "", "/admin/runs/12"],
    ["#/items?class=x", "?states=blocked&class=old", "/items?states=blocked&class=x"],
  ])("%s with search %j becomes %s", (hash, search, target) => {
    expect(legacyHashTarget(hash, search)).toBe(target);
  });

  test.each(["", "#", "#section", "#anchor/with/slash"])("%j is not a route", hash => {
    expect(legacyHashTarget(hash, "")).toBeNull();
  });

  test("a legacy hash URL is replaced by its path without adding a history entry", () => {
    window.history.replaceState(null, "", "/#/runs");
    const length = window.history.length;
    expect(migrateLegacyHash()).toBe(true);
    expect(window.location.pathname).toBe("/runs");
    expect(window.location.hash).toBe("");
    expect(window.history.length).toBe(length);
    expect(migrateLegacyHash()).toBe(false);
  });

  test("the route hook reads a legacy hash as its path and follows one pasted into an open tab", async () => {
    window.history.replaceState(null, "", "/#/admin/runs?after=5");
    const { result } = renderHook(() => useScreenRoute());
    expect(result.current).toBe("/admin/runs?after=5");
    expect(currentRoute()).toBe("/admin/runs?after=5");
    window.history.replaceState(null, "", "/#/runs");
    fireEvent(window, new Event("hashchange"));
    await waitFor(() => expect(result.current).toBe("/runs"));
    expect(window.location.pathname).toBe("/runs");
  });

  test("the old query-only item spelling and run-in-item spelling canonicalize to paths", () => {
    expect(canonicalRoute("/?item=item-a")).toBe("/item/item-a");
    expect(canonicalRoute("/?item=item-a&run=r1&tab=gate")).toBe("/run/r1?tab=gate");
    expect(canonicalRoute("/item/item-a?run=r1")).toBe("/run/r1");
    expect(canonicalRoute("/runs")).toBeNull();
  });
});

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

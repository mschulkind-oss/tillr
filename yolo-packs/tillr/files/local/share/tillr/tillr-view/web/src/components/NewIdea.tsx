import { useEffect, useRef, useState } from "react";
import type { ItemView } from "../models/types";
import { saveIdea } from "../models/api";

type Draft = { words: string; place: "top" | "bottom" | "after"; after: string };
const emptyDraft = (): Draft => ({ words: "", place: "top", after: "" });

/** Browser storage is scoped to the served store, so separate boards cannot share
 * a draft accidentally. Closing the form preserves it; only confirmed saving clears it. */
export function NewIdea({ enabled, items, store, onSaved }: { enabled: boolean; items: ItemView[]; store: string; onSaved: (id: string) => void }) {
  const key = `tillr.idea:${store}`;
  const [initial] = useState(() => {
    try {
      const raw = localStorage.getItem(key);
      if (!raw) return { draft: emptyDraft(), error: "" };
      const value = JSON.parse(raw) as Draft;
      if (typeof value.words !== "string" || !["top", "bottom", "after"].includes(value.place) || typeof value.after !== "string") throw new Error("Stored idea draft has an invalid form; it has not been removed.");
      return { draft: value, error: "" };
    } catch (error) { return { draft: emptyDraft(), error: `Draft storage refused: ${String(error)}` }; }
  });
  const [draft, setDraft] = useState(initial.draft), [storageError, setStorageError] = useState(initial.error);
  const [open, setOpen] = useState(false), [saving, setSaving] = useState(false), [refusal, setRefusal] = useState("");
  const busy = useRef(false), wordsBox = useRef<HTMLTextAreaElement>(null), trigger = useRef<HTMLButtonElement>(null), dialog = useRef<HTMLDivElement>(null);
  const close = () => { setOpen(false); trigger.current?.focus(); };
  const order = items.filter(item => !["not-started", "landed", "closed", "resolved-elsewhere"].includes(item.projected_state.raw_state)).sort((a, b) => {
    const ar = a.rank ?? "", br = b.rank ?? "";
    return ar === br ? a.item_id.localeCompare(b.item_id) : !ar ? 1 : !br ? -1 : ar < br ? -1 : 1;
  });
  useEffect(() => {
    if (!enabled) return;
    const keydown = (event: KeyboardEvent) => {
      const target = event.target;
      if (event.key !== "n" || event.ctrlKey || event.metaKey || event.altKey || event.shiftKey || (target instanceof HTMLElement && (target.matches("input, textarea, select") || target.isContentEditable))) return;
      event.preventDefault(); setOpen(true);
    };
    window.addEventListener("keydown", keydown);
    return () => window.removeEventListener("keydown", keydown);
  }, [enabled]);
  useEffect(() => { if (open) { if (saving) dialog.current?.focus(); else wordsBox.current?.focus(); } }, [open, saving]);
  const change = (next: Draft) => {
    setDraft(next);
    try { localStorage.setItem(key, JSON.stringify(next)); setStorageError(""); }
    catch (error) { setStorageError(`Draft could not be kept across reloads: ${String(error)}`); }
  };
  const save = async () => {
    if (busy.current || !enabled) return;
    if (draft.place === "after" && !draft.after) { setRefusal("Choose the item to place this idea after."); return; }
    busy.current = true; setSaving(true); setRefusal("");
    try {
      const place = draft.place === "after" ? { after: draft.after } : { to: draft.place === "top" ? "first" : "last" };
      const result = await saveIdea(draft.words, place);
      try {
        // Another tab can write this store's draft while the request is pending.
        // A confirmation belongs only to the submitted words and place, never
        // to whatever newer draft happens to occupy the shared storage key.
        if (localStorage.getItem(key) === JSON.stringify(draft)) localStorage.removeItem(key);
        setStorageError("");
      }
      catch (error) { setStorageError(`Idea saved, but the stored draft could not be cleared: ${String(error)}`); }
      setDraft(emptyDraft()); close(); onSaved(result.item_id);
    } catch (error) { setRefusal(error instanceof Error ? error.message : String(error)); }
    finally { busy.current = false; setSaving(false); }
  };
  if (!enabled) return null;
  return <>
    <button ref={trigger} type="button" aria-keyshortcuts="n" onClick={() => setOpen(true)}>New idea</button>
    {!open && storageError ? <p role="alert">{storageError}</p> : null}
    {open ? <div ref={dialog} tabIndex={-1} className="idea-form" role="dialog" aria-modal="true" aria-label="New idea" onKeyDown={event => {
      if (event.key === "Tab") {
        const controls = [...(dialog.current?.querySelectorAll<HTMLElement>("textarea:not(:disabled), input:not(:disabled), select:not(:disabled), button:not(:disabled)") ?? [])];
        const first = controls[0], last = controls.at(-1);
        if (!controls.length) { event.preventDefault(); dialog.current?.focus(); }
        else if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
        else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
      }
      if (event.key === "Escape" && !saving) { event.preventDefault(); close(); }
      if (event.key === "Enter" && event.ctrlKey) { event.preventDefault(); void save(); }
    }}>
      <form onSubmit={event => { event.preventDefault(); void save(); }}>
        <h2>New idea</h2>
        {refusal ? <p role="alert">Save refused: {refusal}</p> : null}
        {storageError ? <p role="alert">{storageError}</p> : null}
        <label>The words<textarea ref={wordsBox} aria-label="The words" rows={12} value={draft.words} disabled={saving} onChange={event => change({ ...draft, words: event.target.value })} /></label>
        <fieldset disabled={saving}><legend>The place</legend>
          {([ ["top", "Top"], ["bottom", "Bottom"], ["after", "After…"] ] as const).map(([place, label]) => <label key={place}><input type="radio" name="idea-place" checked={draft.place === place} onChange={() => change({ ...draft, place })} />{label}</label>)}
          {draft.place === "after" ? <label>After item<select aria-label="After item" value={draft.after} onChange={event => change({ ...draft, after: event.target.value })}><option value="">Choose an item</option>{order.map(item => <option key={item.item_id} value={item.item_id}>{item.title || item.item_id}</option>)}</select></label> : null}
        </fieldset>
        <button type="submit" aria-keyshortcuts="Control+Enter" disabled={saving}>{saving ? "Saving…" : "Save"}</button>
        <button type="button" disabled={saving} onClick={close}>Close (keep draft)</button>
      </form>
    </div> : null}
  </>;
}

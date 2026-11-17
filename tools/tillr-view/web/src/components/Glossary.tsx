import { useEffect, useRef, type ReactNode } from "react";
import glossary from "../models/glossary.json";

type Term = keyof typeof glossary;
const terms = Object.keys(glossary) as Term[];
const pattern = new RegExp(`\\b(${[...terms].sort((a, b) => b.length - a.length).join("|")})(?:es|s)?\\b`, "gi");

export function glossaryTerms(text: string): Term[] {
  return [...new Set([...text.matchAll(pattern)].map(match => match[1].toLowerCase() as Term))];
}

// Attach native hover help without replacing React-owned text nodes. Original titles
// survive; only the glossary's suffix is updated as streamed text changes.
const originalTitles = new WeakMap<Element, string>();
export function attachDefinitions(root: HTMLElement, definitions = glossary) {
  for (const term of terms) if (!definitions[term]) throw new Error(`Missing glossary definition: ${term}`);
  for (const element of [root, ...root.querySelectorAll<HTMLElement>("*")]) {
    if (element.closest("script, style, textarea, [data-glossary-list]")) continue;
    const text = [...element.childNodes].filter(node => node.nodeType === Node.TEXT_NODE).map(node => node.textContent).join(" ");
    const used = glossaryTerms(text);
    if (!used.length && !originalTitles.has(element)) continue;
    if (!originalTitles.has(element)) originalTitles.set(element, element.getAttribute("title") ?? "");
    const title = [originalTitles.get(element), ...used.map(term => definitions[term])].filter(Boolean).join("\n");
    if (title) element.setAttribute("title", title); else element.removeAttribute("title");
    if (used.length) element.setAttribute("data-glossary-terms", used.join(",")); else element.removeAttribute("data-glossary-terms");
  }
}

/** Every page shares one definition source, including data that arrives after mount. */
export function Glossary({ children, className = "glossary-scope" }: { children: ReactNode; className?: string }) {
  const root = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const node = root.current;
    if (!node) return;
    attachDefinitions(node);
    const observer = new MutationObserver(() => attachDefinitions(node));
    observer.observe(node, { childList: true, characterData: true, subtree: true });
    return () => observer.disconnect();
  }, []);
  return <div ref={root} className={className}>
    {children}
    <details data-glossary-list><summary>Words on this page</summary><dl>{terms.map(term => <div key={term}><dt>{term}</dt><dd>{glossary[term]}</dd></div>)}</dl></details>
  </div>;
}

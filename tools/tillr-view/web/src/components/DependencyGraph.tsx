import type { ItemView, WaitingRow } from "../models/types";
import { graphHref } from "./DependencyLinks";
import { dependencyLayout, relatedItems, selfLoopPath } from "../models/dependencyGraph";
import { ClassState } from "./EntityLink";

export function DependencyGraph({ items, waiting, selected, error }: { items?: ItemView[]; waiting?: WaitingRow[]; selected?: string; error?: Error | null }) {
  if (error) return <main className="entity-page"><h1>Dependency graph</h1><p role="alert">Graph refused: {error.message}</p></main>;
  if (!items || !waiting) return <main className="entity-page"><h1>Dependency graph</h1><p>Reading dependencies and classes…</p></main>;
  if (items.some(item => !item.blocked_by)) return <main className="entity-page"><h1>Dependency graph</h1><p role="alert">This server does not supply dependency edges. Rebuild tillr-view to draw the graph.</p></main>;
  const layout = dependencyLayout(items), byID = new Map(layout.nodes.map(node => [node.item.item_id, node]));
  const classes = new Map(waiting.map(row => [row.item_id, row.class]));
  const knownSelection = !!selected && items.some(item => item.item_id === selected);
  const related = knownSelection ? relatedItems(items, selected!) : null;
  return <main className="entity-page"><h1>Dependency graph</h1>
    <p>Dependencies run left to right. Select an item to highlight everything it needs and everything it unblocks. <a href="/graph">Clear selection</a></p>
    {selected ? <p>{knownSelection ? <>Selected: {selected} · <a href={`/item/${encodeURIComponent(selected)}`}>Open item</a></> : <span role="alert">{selected} is not in this snapshot.</span>}</p> : null}
    {!layout.nodes.length ? <p>No unlanded items.</p> : null}
    <div className="dependency-scroll" tabIndex={0} aria-label="Queue dependency graph">
      <div className="dependency-canvas" style={{width: layout.width, height: layout.height}}>
        <svg className="dependency-edges" width={layout.width} height={layout.height} aria-label="Dependency edges">
          <defs><marker id="dependency-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse"><path d="M0 0L10 5L0 10Z" fill="context-stroke" /></marker></defs>
          {layout.edges.map(edge => {
            const to = byID.get(edge.to)!, from = byID.get(edge.from);
            const siblings = layout.edges.filter(candidate => candidate.to === edge.to);
            const y = to.y + (siblings.indexOf(edge) + 1) * to.height / (siblings.length + 1);
            const startX = from ? from.x + from.width : to.x - 36, startY = from ? from.y + from.height / 2 : y;
            const mid = (startX + to.x) / 2;
            const path = edge.from === edge.to ? selfLoopPath(to) : edge.cycle ? `M${startX},${startY} C${startX + 32},${startY} ${to.x - 32},${y - 24} ${to.x},${y}` : `M${startX},${startY} C${mid},${startY} ${mid},${y} ${to.x},${y}`;
            return <g key={`${edge.from}/${edge.to}`} data-self={edge.from === edge.to} data-cycle={edge.cycle} data-landed={edge.met ? edge.from : undefined} data-dimmed={!!related && (!related.has(edge.from) || !related.has(edge.to))}>
              <title>{edge.from} → {edge.to}{edge.met ? " (complete)" : edge.missing ? " (blocker not recorded)" : edge.cycle ? " (cycle)" : ""}</title>
              <path d={path} markerEnd="url(#dependency-arrow)" />
              {!from ? <text x={startX} y={startY - 6}>{edge.met ? "✓" : "?"}</text> : null}
            </g>;
          })}
        </svg>
        {layout.nodes.map(node => {
          const item = node.item, value = classes.get(item.item_id) ?? "not known";
          const lane = item.latest_run?.state === "running" ? item.latest_run.lane_id : "";
          return <article className="dependency-node" key={item.item_id} data-item={item.item_id} data-class={value} data-cycle={node.cycle} data-selected={selected === item.item_id} data-dimmed={!!related && !related.has(item.item_id)} style={{left:node.x, top:node.y, width:node.width, height:node.height}}>
            <ClassState value={value} />{node.cycle ? <strong className="dependency-cycle">cycle</strong> : null}
            <a className="dependency-title" href={graphHref(item.item_id)} aria-label={`Select ${item.title}`}>{node.lines.map((line, index) => <span key={index}>{line}</span>)}</a>
            {lane ? <a href={`/lane/${encodeURIComponent(lane)}`}>{lane}</a> : null}
          </article>;
        })}
      </div>
    </div>
  </main>;
}

import type { ItemView } from "./types";

const terminal = new Set(["landed", "closed", "resolved_elsewhere"]);
export interface DependencyNode {
  item: ItemView; column: number; x: number; y: number; width: number; height: number; lines: string[]; cycle: boolean;
}
export interface DependencyEdge { from: string; to: string; met: boolean; cycle: boolean; missing: boolean }

/** A self-edge travels outside the opaque label box, within the reserved row gap. */
export function selfLoopPath(node: DependencyNode): string {
  const left = node.x, right = left + node.width, top = node.y - 16, entry = node.y + 10;
  return `M${right},${entry} C${right + 24},${entry} ${right + 24},${top} ${right - 24},${top} L${left + 24},${top} C${left - 24},${top} ${left - 24},${entry} ${left},${entry}`;
}

/** Wrap by Unicode code points; fixed-width text keeps labels inside their measured box. */
function titleLines(title: string): string[] {
  const chars = Array.from(title || "Untitled item"), lines: string[] = [];
  while (chars.length) lines.push(chars.splice(0, 28).join(""));
  return lines;
}

/** Collapse strongly connected components (items mutually reachable by dependencies),
 * then assign columns in dependency order. Cycles remain visible, not a layout failure. */
export function dependencyLayout(items: ItemView[]) {
  const all = new Map(items.map(item => [item.item_id, item]));
  const active = items.filter(item => !terminal.has(item.projected_state.state));
  const ids = new Set(active.map(item => item.item_id));
  const parents = new Map(active.map(item => [item.item_id, (item.blocked_by ?? []).filter(id => ids.has(id))]));
  let next = 0;
  const index = new Map<string, number>(), low = new Map<string, number>(), stack: string[] = [], onStack = new Set<string>();
  const groups: string[][] = [], groupOf = new Map<string, number>();
  function visit(id: string) {
    index.set(id, next); low.set(id, next++); stack.push(id); onStack.add(id);
    for (const parent of parents.get(id) ?? []) {
      if (!index.has(parent)) { visit(parent); low.set(id, Math.min(low.get(id)!, low.get(parent)!)); }
      else if (onStack.has(parent)) low.set(id, Math.min(low.get(id)!, index.get(parent)!));
    }
    if (low.get(id) === index.get(id)) {
      const group: string[] = []; let member: string;
      do { member = stack.pop()!; onStack.delete(member); groupOf.set(member, groups.length); group.push(member); } while (member !== id);
      groups.push(group);
    }
  }
  for (const item of active) if (!index.has(item.item_id)) visit(item.item_id);
  const ranks = new Map<number, number>();
  function rank(group: number): number {
    if (ranks.has(group)) return ranks.get(group)!;
    let value = 0;
    for (const id of groups[group]) for (const parent of parents.get(id) ?? []) {
      const other = groupOf.get(parent)!;
      if (other !== group) value = Math.max(value, rank(other) + 1);
    }
    ranks.set(group, value); return value;
  }
  const offsets = new Map<number, number>();
  const nodes: DependencyNode[] = active.map(item => {
    const group = groupOf.get(item.item_id)!, column = rank(group), lines = titleLines(item.title);
    const y = offsets.get(column) ?? 24, height = 68 + lines.length * 18;
    offsets.set(column, y + height + 24);
    return {item, column, x: 52 + column * 304, y, width: 248, height, lines, cycle: groups[group].length > 1 || (parents.get(item.item_id) ?? []).includes(item.item_id)};
  });
  const edges: DependencyEdge[] = active.flatMap(item => (item.blocked_by ?? []).map(from => {
    const source = all.get(from);
    return {from, to: item.item_id, met: !!source && terminal.has(source.projected_state.state), missing: !source,
      cycle: ids.has(from) && groupOf.get(from) === groupOf.get(item.item_id) && (from === item.item_id || groups[groupOf.get(from)!].length > 1)};
  }));
  return {nodes, edges, width: Math.max(360, ...nodes.map(n => n.x + n.width + 28)), height: Math.max(180, ...nodes.map(n => n.y + n.height + 24))};
}

/** Traverse each direction separately: siblings sharing a blocker are not descendants. */
export function relatedItems(items: ItemView[], selected: string): Set<string> {
  const parents = new Map(items.map(item => [item.item_id, item.blocked_by ?? []]));
  const children = new Map<string, string[]>();
  for (const item of items) for (const parent of item.blocked_by ?? []) children.set(parent, [...(children.get(parent) ?? []), item.item_id]);
  const related = new Set([selected]);
  for (const direction of [parents, children]) {
    const seen = new Set<string>(), pending = [selected];
    while (pending.length) { const id = pending.pop()!; if (seen.has(id)) continue; seen.add(id); related.add(id); pending.push(...(direction.get(id) ?? [])); }
  }
  return related;
}

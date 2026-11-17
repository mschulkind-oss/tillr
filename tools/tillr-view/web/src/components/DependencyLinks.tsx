export function graphHref(id: string) { return `#/graph?item=${encodeURIComponent(id)}`; }

export function DependencyLinks({ id, blockedBy, unblocks }: { id: string; blockedBy: string[]; unblocks?: string[] }) {
  const links = (ids: string[]) => ids.length ? ids.map(item => <span key={item}><a href={graphHref(item)}>{item}</a>{" "}</span>) : "nothing";
  return <section aria-label="Dependencies"><h2><a href={graphHref(id)}>Waits on</a></h2>{links(blockedBy)}<h2><a href={graphHref(id)}>Unblocks</a></h2>{unblocks ? links(unblocks) : "Unblocks not recorded by this server."}</section>;
}

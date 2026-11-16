// Every causal hand-off between two programs in one run, in order (design §6.3). The
// unobserved seams the server emits are rows in this table, not omissions: a gap in the
// evidence is a named row an operator can see.

import { EpistemicBadge } from "./EpistemicBadge";
import type { Exchange, ExchangesData } from "../models/types";

export interface InterToolExchangeViewProps {
  /** The run these exchanges belong to, or null when the selection names no run. A read
   * the query hook disabled never answers, so "no run" has to render as a fact rather
   * than as the same placeholder a read in flight shows. */
  runId: string | null;
  exchanges: ExchangesData | undefined;
  selectedExchange: string | null;
  onSelectExchange: (exchangeId: string | null) => void;
}

function clock(ms: number | null, source: string): string {
  if (ms === null) return "no clock";
  const iso = new Date(ms).toISOString();
  return source === "file_mtime" ? `~${iso}` : iso;
}

function exitLabel(exchange: Exchange): string {
  if (exchange.exit_code === null) return exchange.summary ? "" : "no exit code";
  return `exit ${exchange.exit_code}`;
}

export function InterToolExchangeView({
  runId,
  exchanges,
  selectedExchange,
  onSelectExchange,
}: InterToolExchangeViewProps) {
  if (runId === null) {
    return (
      <section className="exchanges">
        <p className="empty">No run is selected, so there are no exchanges to read.</p>
      </section>
    );
  }
  if (!exchanges) return <section className="exchanges">Reading the exchanges…</section>;
  return (
    <section className="exchanges">
      <table className="exchanges__table">
        <thead>
          <tr>
            <th>#</th>
            <th>Time (UTC)</th>
            <th>Layer</th>
            <th>Caller -&gt; Callee</th>
            <th>Channel</th>
            <th>Clock</th>
            <th>Outcome</th>
          </tr>
        </thead>
        <tbody>
          {exchanges.exchanges.map((exchange) => (
            <tr
              key={exchange.exchange_id}
              className={
                exchange.exchange_id === selectedExchange ? "exchange-row exchange-row--selected" : "exchange-row"
              }
              onClick={() =>
                onSelectExchange(
                  exchange.exchange_id === selectedExchange ? null : exchange.exchange_id,
                )
              }
            >
              <td>{exchange.seq}</td>
              <td>{clock(exchange.started_at_ms, exchange.ts_source)}</td>
              <td>{exchange.layer}</td>
              <td>
                {exchange.caller} -&gt; {exchange.callee}
              </td>
              <td>{exchange.channel}</td>
              <td>
                <EpistemicBadge state={exchange.epistemic_state} />
              </td>
              <td>
                {exitLabel(exchange)}
                {exchange.summary ? <span className="exchange-row__summary">{exchange.summary}</span> : null}
              </td>
            </tr>
          ))}
          {exchanges.exchanges.length === 0 ? (
            <tr>
              <td colSpan={7} className="empty">
                the server answered no exchange for this run <EpistemicBadge state="absent" />
              </td>
            </tr>
          ) : null}
        </tbody>
      </table>
      {exchanges.note ? <p className="exchanges__note">{exchanges.note}</p> : null}
    </section>
  );
}

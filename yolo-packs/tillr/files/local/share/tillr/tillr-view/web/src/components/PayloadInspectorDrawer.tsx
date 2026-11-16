// The right-hand inspector: the selected exchange, and one artifact's body read through
// the artifact route. A record that is absent or unreadable is shown as that, with the
// paths the server searched — never as an empty payload.

import { EpistemicBadge } from "./EpistemicBadge";
import { RefusalState, refusalCode } from "./RefusalState";
import type { RefusalSentence } from "./RefusalState";
import type { ArtifactData, Exchange } from "../models/types";

/** The artifact route's own refusals. A code that is not here falls through to the shared
 * component's `the face refused: <code>` branch. */
const ARTIFACT_REFUSALS: Record<string, RefusalSentence> = {
  "unknown-kind": {
    title: "The store does not know this artifact kind",
    body: "The route names an artifact this face has no reader for, so it says so rather than showing an empty body.",
    tone: "notice--working",
    retry: false,
  },
};

export interface PayloadInspectorDrawerProps {
  exchange: Exchange | null;
  artifact: ArtifactData | undefined;
  artifactKind: string | null;
  artifactError: Error | null;
  onClose: () => void;
}

function ArtifactBody({ artifact }: { artifact: ArtifactData }) {
  const payload = artifact.artifact;
  return (
    <div className="inspector__artifact">
      <h3>
        {payload.kind} <EpistemicBadge state={payload.epistemic_state} />
      </h3>
      {payload.present ? (
        <>
          {payload.clobber_warning ? (
            <p className="inspector__warning">{payload.clobber_warning}</p>
          ) : null}
          <p className="inspector__meta">
            <code>{payload.path}</code> · {payload.size_bytes ?? 0} bytes · sha256{" "}
            {payload.sha256?.slice(0, 12) ?? "not recorded"}
            {payload.mtime_ms ? ` · mtime ${new Date(payload.mtime_ms).toISOString()}` : ""}
          </p>
          <pre className="inspector__body">{payload.body ?? "the server sent no body"}</pre>
          {payload.files && payload.files.length > 0 ? (
            <ul className="inspector__files">
              {payload.files.map((file) => (
                <li key={file.path}>
                  <code>{file.path}</code> · {file.size_bytes} bytes{" "}
                  <EpistemicBadge state={file.epistemic_state} />
                </li>
              ))}
            </ul>
          ) : null}
        </>
      ) : (
        <div className="inspector__absent">
          <p>{payload.note ?? "the server reported this artifact absent"}</p>
          {payload.searched_paths && payload.searched_paths.length > 0 ? (
            <ul>
              {payload.searched_paths.map((path) => (
                <li key={path}>
                  looked for <code>{path}</code>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      )}
    </div>
  );
}

export function PayloadInspectorDrawer({
  exchange,
  artifact,
  artifactKind,
  artifactError,
  onClose,
}: PayloadInspectorDrawerProps) {
  if (!exchange && !artifactKind) {
    return (
      <aside className="inspector inspector--empty">
        Select an exchange or open an artifact to inspect it.
      </aside>
    );
  }
  return (
    <aside className="inspector">
      <button type="button" className="inspector__close" onClick={onClose}>
        close
      </button>
      {exchange ? (
        <div className="inspector__exchange">
          <h2>
            Exchange #{exchange.seq} <EpistemicBadge state={exchange.epistemic_state} />
          </h2>
          <p className="inspector__meta">
            {exchange.caller} -&gt; {exchange.callee}
          </p>
          <dl>
            <dt>Channel</dt>
            <dd>{exchange.channel}</dd>
            <dt>Layer</dt>
            <dd>{exchange.layer}</dd>
            <dt>Exit</dt>
            <dd>{exchange.exit_code === null ? "not recorded" : exchange.exit_code}</dd>
            <dt>Clock source</dt>
            <dd>{exchange.ts_source}</dd>
            <dt>Input</dt>
            <dd>
              <code>{exchange.request_ref || "not recorded"}</code>
            </dd>
            <dt>Output</dt>
            <dd>
              <code>{exchange.response_ref || "not recorded"}</code>
            </dd>
          </dl>
          {exchange.summary ? <p className="inspector__summary">{exchange.summary}</p> : null}
          {exchange.inline_preview === null ? (
            <p className="inspector__meta">
              no preview: the record could not be read, which is not an empty record{" "}
              <EpistemicBadge state="unreadable" />
            </p>
          ) : (
            <pre className="inspector__body">{exchange.inline_preview}</pre>
          )}
        </div>
      ) : null}
      {artifactError ? (
        <div className="inspector__absent">
          <RefusalState
            code={refusalCode(artifactError)}
            message={artifactError.message}
            sentences={ARTIFACT_REFUSALS}
          />
          <EpistemicBadge state="unreadable" />
        </div>
      ) : null}
      {artifactKind && !artifact && !artifactError ? (
        <p className="inspector__meta">Reading {artifactKind}…</p>
      ) : null}
      {artifact ? <ArtifactBody artifact={artifact} /> : null}
    </aside>
  );
}

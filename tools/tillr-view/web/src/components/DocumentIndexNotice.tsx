import type { DocumentIndexStatus } from "../models/types";

/** Says plainly when the planner's questions came from a document index older than the
 * documents (a rebuild is running, or the last one failed), and how old that index is. */
export function DocumentIndexNotice({ status }: { status?: DocumentIndexStatus }) {
  if (!status || status.state === "current") return null;
  const failed = status.state === "unavailable" || status.state === "failed";
  return (
    <p role="status" className={`notice ${failed ? "notice--blocked" : "notice--accent"} document-index-notice`}>
      {status.message ?? "The document index is being refreshed."}
    </p>
  );
}

// The verification claims a run's verdict rested on (design §6.2 ClaimAuditBanner). The
// server marks a claim `unobserved_seam` when it was recorded as unverified; that is the
// stale-green trap, so it renders amber and says why rather than passing quietly.

import { EpistemicBadge } from "./EpistemicBadge";
import type { ClaimView, RunData } from "../models/types";

export interface ClaimAuditBannerProps {
  run: RunData | undefined;
}

/** claimLabel is the claim's own text, or the server's spelling when it sent no text. */
function claimLabel(claim: ClaimView): string {
  return claim.claim || "unnamed claim";
}

function claimClass(claim: ClaimView): string {
  if (claim.status === "verified" && claim.epistemic_state === "observed") return "claim--verified";
  if (claim.status === "unverified" || claim.epistemic_state === "unobserved_seam") {
    return "claim--unverified";
  }
  if (claim.status === "disproved") return "claim--disproved";
  return "claim--unknown";
}

export function ClaimAuditBanner({ run }: ClaimAuditBannerProps) {
  if (!run) return null;
  if (run.claim_checks.length === 0) {
    return (
      <section className="claim-banner claim-banner--empty">
        No claim was checked for this run. <EpistemicBadge state="absent" />
        <span className="claim-banner__note">
          the claim rows are tillr-trace's reading of the run's artifacts; with none read there is nothing to verify
        </span>
      </section>
    );
  }
  return (
    <section className="claim-banner">
      <h2>Claim audit</h2>
      <ul className="claim-banner__list">
        {run.claim_checks.map((claim) => (
          <li key={claim.claim} className={`claim ${claimClass(claim)}`}>
            <span className="claim__name">{claimLabel(claim)}</span>
            <span className="claim__status">{claim.status.toUpperCase()}</span>
            <EpistemicBadge state={claim.epistemic_state} />
            {claim.reason ? <span className="claim__reason">{claim.reason}</span> : null}
            <span className="claim__observed">{claim.observed_at || "no observations recorded"}</span>
          </li>
        ))}
      </ul>
    </section>
  );
}

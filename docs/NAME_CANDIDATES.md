# Name Candidates for "tillr"

> **Current working name:** tillr
> **What the tool does:** A human-in-the-loop project management CLI+web tool for agentic software development. It sits between humans and AI agents, steering work through iteration cycles.

---

## Decision Criteria

The name must evoke **two complementary ideas**:

1. **Sensing / Feedback** — understanding the pulse of the project. Monitoring health, reading signals, knowing the state of things. Like a navigator checking instruments or a doctor reading vitals.
2. **Steering / Direction** — driving where the project goes next. Setting course, making decisions, guiding agents. Like a helmsperson at the tiller or a conductor raising the baton.

A name that captures only one side (e.g., "Pulse" is sensing-only; "Dispatch" is steering-only) is weaker than one that implies the full loop: sense the state, then steer the course.

Secondary criteria:
- **Namespace availability** — can we own the name on npm, PyPI, crates.io, Homebrew, GitHub, and Docker Hub without confusion?
- **Memorability** — short, easy to spell, pleasant to type in a terminal
- **Distinctiveness** — not overloaded with existing dev tool associations

---

## Top 5 Recommendations (Re-ranked with Availability)

| Rank | Name | Why | Availability |
|------|------|-----|-------------|
| 1 | **Tiller** (use **tillr** or **tiller-cli**) | A tiller is the lever that steers a boat — but you steer by *feeling* the water through it. It is one of the few nautical terms that inherently combines sensing feedback with directing course. The core name has conflicts, but **tillr** is clean everywhere and **tiller-cli** / **tiller-dev** are fully available. | See detailed breakdown below |
| 2 | **Bearing** | Your bearing is your direction of travel, but you *take* a bearing by reading instruments — sense then steer. "Get your bearings" means orient yourself before proceeding. Works as both noun and verb. | npm: available. PyPI: available. crates.io: available. Homebrew: available. GitHub org: taken (mostly empty). Low conflict overall. |
| 3 | **Baton** | A conductor's baton directs the orchestra, but a conductor listens intensely to shape the performance — the baton is the interface between sensing the music and steering it. Also evokes relay-race handoffs (human to agent). | npm: available. PyPI: available. crates.io: taken (small crate). Homebrew: available. GitHub: user taken. Low conflict. |
| 4 | **Relay** | Captures the handoff nature between human and agent. A relay requires reading the runner's pace (sensing) and timing the handoff (steering). Clean, short, widely understood. | npm: taken (old, low downloads). PyPI: taken (MQTT relay, niche). crates.io: available. Homebrew: available. Moderate conflict — would benefit from **relay-cli** or **relay-dev**. |
| 5 | **Locus** | Latin for "place" — your locus of control is where decisions originate. Implies a central point where information flows in (sensing) and decisions flow out (steering). Short, distinctive, no dev tool baggage. | npm: taken (old geolocation lib, dead). PyPI: taken (genetic analysis tool, niche). crates.io: available. Homebrew: available. Consider **locus-cli** or **locus-dev**. |

### Tiller Availability Deep Dive

| Registry | `tiller` | `tillr` | `tiller-cli` | `tiller-dev` | `go-tiller` |
|----------|----------|---------|---------------|---------------|-------------|
| npm | TAKEN (MongoDB ODM, 8 dl/wk, dead) | Available | Available | Available | n/a |
| PyPI | TAKEN (toy sailboat plugin, dormant) | Available | Available | Available | n/a |
| crates.io | TAKEN (static site gen, 7K downloads) | Available | Available | Available | n/a |
| Go modules | Available but Helm v2 association | n/a | n/a | n/a | Available |
| Homebrew | Available | Available | Available | Available | Available |
| GitHub user | TAKEN (personal account) | TAKEN (0 repos) | Available | Available | Available |
| Docker Hub | HIGH CONFLICT (helmpack/tiller, 12.7M pulls from Helm v2) | Available | Available | Available | Available |

**Recommendation:** Use **tillr** as the primary package/binary name. It is clean on every registry, avoids the Helm v2 Docker Hub collision entirely, and the dropped vowel is a familiar pattern in dev tools (Flickr, Tumblr, Buildkite's buildkr, etc.).

---

## Nautical / Steering

| # | Name | Rationale | Dual Meaning (Sense + Steer)? | Conflicts | Availability |
|---|------|-----------|-----------------------------|-----------|-------------|
| 1 | **Tiller** | Lever that steers a boat | ✅ Feel the water + direct the course | See deep dive above | Use **tillr** — all clear |
| 2 | **Helm** | Ship's steering station | ✅ Monitor instruments + steer | ⚠️ **Major**: Helm (K8s package manager) | npm/PyPI/crates all taken by Helm ecosystem. Unusable. |
| 3 | **Rudder** | Steering mechanism | ~Partial: more steering than sensing | ⚠️ Rudder (customer data platform) | npm: taken. PyPI: taken. crates: available. Moderate conflict. |
| 4 | **Bearing** | Navigational direction | ✅ Take a bearing (sense) + hold a bearing (steer) | ✅ No major conflicts | npm/PyPI/crates: available. Low conflict. |
| 5 | **Heading** | Direction of travel | ~Partial: more about direction | Minor: too generic as a word | npm: taken. Generic term makes searching hard. |
| 6 | **Coxswain** | Person who steers a rowing crew | ✅ Reads the race + directs the crew | ❌ Hard to spell and pronounce | Available everywhere but unusable for UX reasons. |
| 7 | **Bridge** | Ship's command center | ✅ Monitoring station + command post | ⚠️ Very overloaded term in tech | npm: taken. Extremely generic. |
| 8 | **Compass** | Navigation instrument | ~Partial: more about sensing | ⚠️ Compass (MongoDB GUI), JetBrains plugin | npm: taken. PyPI: taken. High conflict. |
| 9 | **Pilotage** | Act of piloting a vessel | ✅ Reading conditions + guiding | Minor: unusual word, long | Available but impractical to type. |
| 10 | **Sextant** | Celestial navigation tool | ~Partial: mostly sensing/measurement | ✅ No major conflicts | npm/PyPI: available. Obscure but distinctive. |
| 11 | **Starboard** | Right side of a ship | ❌ Doesn't evoke steering | ⚠️ Starboard (security tool) | Taken in multiple places. |
| 12 | **Windward** | Direction the wind blows from | ~Partial: strategic positioning | ✅ No major conflicts | npm: available. PyPI: available. Long to type. |

## Orchestration / Conducting

| # | Name | Rationale | Dual Meaning (Sense + Steer)? | Conflicts | Availability |
|---|------|-----------|-----------------------------|-----------|-------------|
| 13 | **Conductor** | Leads an orchestra | ✅ Listens + directs | ⚠️ **Major**: Conductor (Netflix orchestrator) | Taken everywhere. Unusable. |
| 14 | **Maestro** | Master conductor | ✅ Deep listening + expert direction | ⚠️ Maestro (various startups, Meta's codegen) | npm: taken. PyPI: taken. High conflict. |
| 15 | **Baton** | Conductor's directing tool | ✅ Interface between listening and directing | ✅ Minor conflicts only | npm/PyPI: available. crates: taken (small). Low conflict. |
| 16 | **Relay** | Passing work between runners | ✅ Read pace + time handoff | ✅ No major conflicts in CLI tools | npm: taken (old). PyPI: taken (niche). Use **relay-cli**. |
| 17 | **Dispatch** | Send off to a destination | ❌ Steering only — no sensing | ⚠️ Generic, many tools use it | npm: taken. PyPI: taken. High conflict. |
| 18 | **Cue** | Signal to act | ~Partial: response to observation | Minor: short, hard to search | npm: taken. Too generic for search. |
| 19 | **Overture** | Opening of a composition | ❌ Implies beginning only | ✅ No major conflicts | npm/PyPI: available. Doesn't fit the dual metaphor. |
| 20 | **Ensemble** | Group performing together | ~Partial: collaborative but passive | Minor: long-ish | npm: taken. PyPI: taken. |

## Project Management

| # | Name | Rationale | Dual Meaning (Sense + Steer)? | Conflicts | Availability |
|---|------|-----------|-----------------------------|-----------|-------------|
| 21 | **Cadence** | Rhythm of work cycles | ~Partial: more about pace than direction | ⚠️ Cadence (Uber workflow engine) | npm: taken. PyPI: taken. Moderate conflict. |
| 22 | **Tempo** | Speed/pace of work | ❌ Pace only — no sensing or direction | ⚠️ Tempo (Spotify acquisition, various tools) | npm: taken. PyPI: taken. High conflict. |
| 23 | **Pulse** | Heartbeat of a project | ❌ Sensing only — no steering | ⚠️ Overused in SaaS products | npm: taken. PyPI: taken. High conflict. |
| 24 | **Sprint** | Iteration cycle | ❌ Neither sensing nor steering | ⚠️ Very overloaded in dev tools | Unusable. |
| 25 | **Iteration** | Cycle of work | ❌ Too literal, no metaphor | ❌ Too generic, long | Unusable. |
| 26 | **Cycle** | Repeating process | ❌ Neither sensing nor steering | ⚠️ Generic term | npm: taken. Too generic. |
| 27 | **Epoch** | Distinct period of time | ❌ Time-marking only | Minor: ML training connotation | npm: taken. PyPI: taken. |
| 28 | **Milestone** | Significant checkpoint | ❌ Destination marker, not a control | ⚠️ GitHub already uses this term | Overloaded. |

## Human-Agent Collaboration

| # | Name | Rationale | Dual Meaning (Sense + Steer)? | Conflicts | Availability |
|---|------|-----------|-----------------------------|-----------|-------------|
| 29 | **Turnstile** | Controlled passage gate | ~Partial: gate implies judgment but not sensing | ⚠️ Cloudflare Turnstile (CAPTCHA) | npm: taken. PyPI: available. Cloudflare association is strong. |
| 30 | **Handoff** | Passing work between parties | ~Partial: implies exchange but not sensing | ✅ No major conflicts | npm: available. PyPI: available. crates: available. Clean. |
| 31 | **Checkpoint** | Verification point | ~Partial: review implies sensing but not steering | ⚠️ ML checkpointing, generic | npm: taken. PyPI: taken. |
| 32 | **Sentry** | Guard/watchperson | ~Partial: observes but doesn't steer | ⚠️ **Major**: Sentry (error monitoring) | Completely unusable. |
| 33 | **Gatekeeper** | Controls access/approval | ~Partial: judges but doesn't sense trends | ⚠️ Overused term | npm: taken. Long to type. |
| 34 | **Arbiter** | Decision maker | ~Partial: decides but doesn't sense | ✅ Minor conflicts only | npm: available. PyPI: available. crates: available. Clean. |
| 35 | **Steward** | Caretaker/manager | ✅ Monitors welfare + manages direction | ✅ No major conflicts | npm: taken (low downloads). PyPI: available. crates: available. |
| 36 | **Marshal** | Organizer/director | ~Partial: more about directing | Minor: Go's marshal/unmarshal | npm: taken. Go association makes it confusing. |
| 37 | **Liaison** | Go-between for two parties | ✅ Gathers info from both sides + facilitates | ✅ No major conflicts | npm: available. PyPI: available. Clean but long. |

## Development Tillr

| # | Name | Rationale | Dual Meaning (Sense + Steer)? | Conflicts | Availability |
|---|------|-----------|-----------------------------|-----------|-------------|
| 38 | **Forge** | Where things are made | ❌ Creation only | ⚠️ **Major**: Forge (Autodesk, multiple tools) | Unusable. |
| 39 | **Foundry** | Where things are cast | ❌ Creation only | ⚠️ Foundry (Palantir, Ethereum tool) | Unusable. |
| 40 | **Lathe** | Shaping tool | ~Partial: craftsperson senses material | ✅ No major conflicts | npm: available. PyPI: available. crates: available. Clean. |
| 41 | **Anvil** | Where things are hammered | ❌ Crafting only | ⚠️ Anvil (web framework) | npm: taken. |
| 42 | **Crucible** | Vessel for transformation | ~Partial: testing/refining implies observation | ⚠️ Crucible (Atlassian code review — discontinued) | npm: taken. PyPI: taken. |
| 43 | **Kiln** | Firing/hardening | ❌ Processing only | ⚠️ Kiln (Fog Creek VCS — defunct) | npm: taken (dead). PyPI: available. Residual association. |
| 44 | **Loom** | Weaving threads together | ~Partial: weaver watches the pattern | ⚠️ **Major**: Loom (video messaging) | Unusable. |

## Other Creative Names

| # | Name | Rationale | Dual Meaning (Sense + Steer)? | Conflicts | Availability |
|---|------|-----------|-----------------------------|-----------|-------------|
| 45 | **Wheelhouse** | Where you steer from | ✅ Monitoring station + steering position | ✅ No major conflicts, fun idiom | npm: taken (dead). PyPI: available. Long but memorable. |
| 46 | **Flywheel** | Self-reinforcing momentum | ~Partial: momentum more than sensing | ⚠️ Flywheel (WordPress hosting) | npm: taken. PyPI: taken. |
| 47 | **Ratchet** | Forward-only progress | ❌ Mechanism, no sensing | ✅ No major conflicts | npm: taken (low downloads). PyPI: available. crates: available. |
| 48 | **Scaffold** | Temporary support structure | ❌ Neither sensing nor steering | ⚠️ Overused in dev tools | Unusable. |
| 49 | **Locus** | Center of control | ✅ Where information converges + decisions originate | ✅ No major conflicts | npm: taken (dead). PyPI: taken (niche). Use **locus-cli**. |
| 50 | **Nexus** | Connection point | ~Partial: hub but not clearly sensing/steering | ⚠️ Nexus (Sonatype, many others) | Unusable. |
| 51 | **Quorum** | Minimum needed for decisions | ~Partial: agreement implies evaluation | ✅ No major conflicts in dev | npm: taken (dead). PyPI: available. crates: available. Distinctive. |
| 52 | **Concord** | Agreement/harmony | ~Partial: alignment but not active sensing | Minor: Concord (various) | npm: taken. PyPI: available. |

---

## Summary: Best Names for Sense + Steer

The strongest candidates that capture **both feedback/sensing AND steering/direction**, ranked by combined conceptual fit and namespace availability:

| Name | Sense + Steer | Availability | Recommended Form |
|------|--------------|-------------|-----------------|
| **Tiller** | ★★★★★ Feel water + steer course | Conflicted as `tiller` | **tillr** (all registries clear) |
| **Bearing** | ★★★★☆ Take bearings + hold bearing | ★★★★★ Clean everywhere | **bearing** |
| **Baton** | ★★★★☆ Listen + direct | ★★★★☆ Mostly clean | **baton** |
| **Locus** | ★★★☆☆ Info convergence + decision origin | Needs variation | **locus-cli** |
| **Steward** | ★★★☆☆ Monitor welfare + manage | ★★★★☆ Mostly clean | **steward** |

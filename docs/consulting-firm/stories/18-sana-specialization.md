# 18. Sana — Specialization and Team Composition

**Context:** Sana notices that features touching the frontend have a
33% rejection rate vs 11% for backend features. She wonders if agent
configuration matters — are generic agents good enough for everything?

**What happens:**

1. Sana asks for a breakdown:

   ```
   tillr stats --by-domain
   ```

   ```
   Domain Analysis — Last 30 Features
   ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

   Backend (API, DB):    18 features, 11% rejection rate
   Frontend (React, CSS): 12 features, 33% rejection rate

   Rejection reasons (frontend):
     Layout/design mismatch:  4 (most common)
     Accessibility missing:   2
     State management issues: 2
   ```

2. The pattern is clear. Sana configures a specialized frontend agent
   role:

   ```
   tillr role create frontend-engineer \
     --cycle-step implement \
     --domain-tags frontend,ui \
     --context-additions "Always use Chrome DevTools to verify visual
       output. Check responsive behavior at 3 breakpoints. Match
       existing component library patterns in web/src/components/."
   ```

3. Future frontend features get assigned to the frontend-engineer role.
   The context packet includes the specialized instructions plus all
   frontend-specific patterns and reviewer feedback.

4. After 10 features with the specialized role:

   ```
   Frontend rejection rate: 33% → 12%
     Layout/design: 4 → 1 (Chrome DevTools check catches most)
     Accessibility: 2 → 0 (specialized instructions include a11y)
   ```

   **Gap:** Sana created the role manually with `--context-additions`.
   But the ideal context additions should be *derived* from the
   rejection reasons. The system analyzed "layout/design mismatch" as
   the top rejection reason — it should propose: "add Chrome DevTools
   screenshot comparison to frontend agent context." The role creation
   should be data-driven, not manual.

**What would trip her up:**
- Domain tagging needs to be accurate. If a feature is tagged `frontend`
  but it's mostly a backend API change with a small UI tweak, the
  frontend-engineer context is wasted and the backend context is missing.
  Tag quality is a prerequisite for specialization.
- Over-specialization: if Sana creates 5 roles (frontend, backend, DB,
  infra, docs), the system needs to match features to roles correctly.
  Misassignment wastes the specialized context.

**What makes this work:**
- Data drove the specialization decision.
- Agent roles are configuration, not different models. Same LLM,
  different context.
- The specialization compounds: frontend patterns accumulate and only
  frontend agents receive them.

---

« [All stories](./README.md) · [Consulting-firm overview](../README.md)

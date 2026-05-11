# Software Engineering Books

Distilled decision rules from classic software engineering books, organized by scenario.

## How to use

This skill provides a **routing table** below. When your task matches a scenario, read the corresponding reference file for detailed decision rules and checklists.

Do NOT load all references at once. Load only what the current task needs.

## Quick reference — cross-book principles

These seven principles appear across most books. Internalize them; they are the overlap.

1. **Local reasoning**: A reader should understand the path without reconstructing hidden state, wide jumps, or naming trivia.
2. **Small verified steps**: Work in buildable, testable, reviewable increments. Preserve behavior; never disguise a rewrite as cleanup.
3. **Deep modules**: Hide meaningful complexity behind small semantic interfaces. Reject wrappers that add names without reducing reader burden.
4. **Single source of truth**: One authoritative owner for each piece of system knowledge. Derive, generate, or trace the rest.
5. **Names are design**: Precise abstraction names, one term per concept. When naming is hard, treat it as design evidence.
6. **Explicit failure boundaries**: Make contracts, invariants, retries, ownership, and cleanup visible. Assume production mess.
7. **Details inward, policy protected**: Business rules stay independent; volatile mechanisms remain replaceable behind boundaries.

## Routing table

| Scenario | Read this reference | Key focus |
|---|---|---|
| Writing or reviewing everyday code — readability, naming, functions | `clean-code.mini.md` | Local reasoning, precise names, small functions, explicit mutation |
| Module/API design feels awkward, spreads widely, or leaks internals | `philosophy-of-sd.mini.md` | Deep modules, information hiding, reduce cognitive load |
| Changing existing code without breaking behavior | `refactoring.mini.md` | Small behavior-preserving steps, safety net, stop when clear enough |
| Touching untested or poorly understood legacy code | `legacy-code.mini.md` | Characterize first, create seams, break blocking dependencies |
| General engineering discipline — accountability, automation, feedback | `pragmatic-programmer.mini.md` | Orthogonality, reversibility, automate repeatable work, broken windows |
| Business complexity, domain language, bounded contexts | `ddd.mini.md` | Ubiquitous Language, Aggregates, context maps, Core Domain |
| Keeping business rules independent from frameworks, DB, UI | `clean-architecture.mini.md` | Dependency rule, inward dependencies, humble adapters |
| Enterprise layering, persistence patterns, transaction boundaries | `peaa.mini.md` | Layer responsibilities, Domain Model vs Transaction Script, repositories |
| Data correctness, replication, consistency, schema evolution | `ddia.mini.md` | Source of truth, idempotency, ordering, compatibility |
| Production reliability — timeouts, retries, circuit breakers, overload | `release-it.mini.md` | Bounded resources, fail visibly, isolate failures, validate input |
| Construction quality — control flow, validation, debugging, tuning | `code-complete.mini.md` | Reader-first code, trust boundaries, evidence-based debugging |

## When multiple references apply

Common combinations:

- **Adding a feature to legacy code**: `legacy-code.mini.md` → `refactoring.mini.md` → `clean-code.mini.md`
- **Designing a new module/API**: `philosophy-of-sd.mini.md` → `clean-architecture.mini.md`
- **Modeling a complex domain**: `ddd.mini.md` → `clean-architecture.mini.md` → `peaa.mini.md`
- **Building a data pipeline or service**: `ddia.mini.md` → `release-it.mini.md`
- **General coding task, unsure which to load**: Start with `clean-code.mini.md` + `pragmatic-programmer.mini.md`

## Reference files

All files are in the `reference/` directory alongside this skill.
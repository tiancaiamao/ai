---
name: improve-codebase-architecture
description: Find deepening opportunities in a codebase. Use when the user wants to improve architecture, find refactoring opportunities, consolidate tightly-coupled modules, or make a codebase more testable and AI-navigable.
---

# Improve Codebase Architecture

Surface architectural friction and propose **deepening opportunities** — refactors that turn shallow modules into deep ones. The aim is testability and AI-navigability.

## Glossary

Use these terms exactly in every suggestion. Consistent language is the point.

- **Module** — anything with an interface and an implementation (function, type, package, slice). Deliberately scale-agnostic.
- **Interface** — everything a caller must know to use the module: types, invariants, error modes, ordering, config. Not just the Go `interface{}` or type signature.
- **Implementation** — the code inside the module.
- **Depth** — leverage at the interface: a lot of behaviour behind a small interface. **Deep** = high leverage. **Shallow** = interface nearly as complex as the implementation.
- **Seam** — where an interface lives; a place behaviour can be altered without editing in place.
- **Adapter** — a concrete thing satisfying an interface at a seam (e.g., a struct implementing a Go interface).
- **Leverage** — what callers get from depth: more capability per unit of interface they must learn.
- **Locality** — what maintainers get from depth: change, bugs, knowledge concentrated in one place.

Key principles:

- **Deletion test**: imagine deleting the module. If complexity vanishes, it was a pass-through. If complexity reappears across N callers, it was earning its keep.
- **The interface is the test surface.**
- **One adapter = hypothetical seam. Two adapters = real seam.**

## Process

### 1. Explore

Read the project's context first:

1. **`AGENTS.md`** — project conventions, high-value code paths, guardrails.
2. **`docs/adr/`** — existing architectural decisions in the area you're touching.
3. **Domain glossary** — if `CONTEXT.md` exists at repo root, read it for domain vocabulary. If not, infer domain terms from package names, type names, and README.

Then use `read`, `grep`, and `bash` to walk the codebase. Don't follow rigid heuristics — explore organically and note where you experience friction:

- Where does understanding one concept require bouncing between many small modules?
- Where are modules **shallow** — interface nearly as complex as the implementation?
- Where have pure functions been extracted just for testability, but the real bugs hide in how they're called (no **locality**)?
- Where do tightly-coupled modules leak across their seams?
- Which parts of the codebase are untested, or hard to test through their current interface?

Apply the **deletion test** to anything you suspect is shallow: would deleting it concentrate complexity, or just move it? A "yes, concentrates" is the signal you want.

### 2. Present candidates

Present a numbered list of deepening opportunities. For each candidate:

- **Files** — which files/packages are involved
- **Problem** — why the current architecture is causing friction
- **Solution** — plain English description of what would change
- **Benefits** — explained in terms of locality and leverage, and also in how tests would improve

**ADR conflicts**: if a candidate contradicts an existing ADR, only surface it when the friction is real enough to warrant revisiting the ADR. Mark it clearly (e.g. _"contradicts ADR-0001 — but worth reopening because…"_). Don't list every theoretical refactor an ADR forbids.

Do NOT propose interfaces yet. Ask the user: "Which of these would you like to explore?"

### 3. Grilling loop

Once the user picks a candidate, drop into a grilling conversation. Walk the design tree with them — constraints, dependencies, the shape of the deepened module, what sits behind the seam, what tests survive.

Side effects happen inline as decisions crystallize:

- **Naming a deepened module after a concept not in any existing glossary?** Propose adding the term to `CONTEXT.md` (or `AGENTS.md` if no `CONTEXT.md` exists). Create the file lazily if it doesn't exist.
- **Sharpening a fuzzy term during the conversation?** Update the glossary right there.
- **User rejects the candidate with a load-bearing reason?** Offer an ADR: _"Want me to record this as an ADR so future architecture reviews don't re-suggest it?"_ Only offer when the reason would actually be needed by a future explorer.

## Deepening Guide

How to deepen a cluster of shallow modules safely, given its dependencies.

### Dependency categories

When assessing a candidate for deepening, classify its dependencies. The category determines how the deepened module is tested across its seam.

| Category | Description | Strategy |
|----------|-------------|----------|
| **In-process** | Pure computation, in-memory state, no I/O | Always deepenable — merge modules, test through new interface directly. No adapter needed. |
| **Local-substitutable** | Dependencies with local test stand-ins (sqlite for postgres, in-memory fs) | Deepenable if stand-in exists. Test with stand-in in test suite. Seam is internal. |
| **Remote but owned** | Your own services across network boundary | Define a **port** (Go interface) at the seam. Deep module owns logic; transport injected as **adapter**. Tests use in-memory adapter. |
| **True external** | Third-party services you don't control | Deepened module takes external dependency as injected port; tests provide a mock adapter. |

### Seam discipline

- **One adapter = hypothetical seam. Two adapters = real one.** Don't introduce a port unless at least two adapters are justified (typically production + test). A single-adapter seam is just indirection.
- **Internal seams vs external seams.** A deep module can have internal seams (private to its implementation, used by its own tests) as well as the external seam at its interface. Don't expose internal seams through the interface just because tests use them.

### Testing strategy

- Old unit tests on shallow modules become waste once tests at the deepened module's interface exist — delete them.
- Write new tests at the deepened module's interface. The **interface is the test surface**.
- Tests assert on observable outcomes through the interface, not internal state.
- Tests should survive internal refactors — they describe behaviour, not implementation.

## Interface Design (Design It Twice)

When the user wants to explore alternative interfaces for a chosen deepening candidate:

### 1. Frame the problem space

Write a user-facing explanation of:
- The constraints any new interface would need to satisfy
- The dependencies and their categories
- A rough illustrative code sketch to ground the constraints — not a proposal, just concrete constraints

Show this to the user, then proceed to Step 2.

### 2. Explore alternatives

Present 2-3 **radically different** interface designs for the deepened module:

- **Design A**: Minimize the interface — 1-3 methods max. Maximise leverage per method.
- **Design B**: Optimise for the most common caller — make the default case trivial.
- **Design C**: Design around ports & adapters for cross-seam dependencies.

Each design includes:
1. Interface definition (Go types, methods, invariants, error modes)
2. Usage example showing how callers use it
3. What the implementation hides behind the seam
4. Dependency strategy and adapters
5. Trade-offs — where leverage is high, where it's thin

### 3. Present and compare

Present designs sequentially, then compare by **depth** (leverage at the interface), **locality** (where change concentrates), and **seam placement**.

Give your own recommendation: which design is strongest and why. Be opinionated — the user wants a strong read, not a menu.

## ADR Recording

When offering to record a decision as an ADR in `docs/adr/`:

All three of these must be true before offering:

1. **Hard to reverse** — the cost of changing your mind later is meaningful
2. **Surprising without context** — a future reader will wonder "why did they do it this way?"
3. **The result of a real trade-off** — there were genuine alternatives

**ADR format** — scan `docs/adr/` for the highest existing number and increment:

```markdown
# {Short title}

{1-3 sentences: what's the context, what did we decide, and why.}
```

Keep it concise. An ADR can be a single paragraph. Only add Status/Options/Consequences sections when they add genuine value.

## CONTEXT.md Format (Optional)

If the project benefits from a domain glossary, create `CONTEXT.md` at repo root:

```markdown
# {Project Name}

{One or two sentences about the project's domain.}

## Language

**Term**:
Concise definition.
_Avoid_: synonym1, synonym2

## Relationships

- A **Term** relates to **OtherTerm** via ...

## Flagged ambiguities

- "word" was used to mean both X and Y — resolved: these are distinct concepts.
```

Rules:
- Be opinionated — pick the best term, list others as aliases to avoid.
- Keep definitions to one sentence.
- Only include project-specific domain concepts, not general programming terms.
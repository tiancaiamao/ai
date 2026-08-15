# OBEY Domain-Driven Design — Synthesized from Blue Book (Evans), Distilled (Vernon), and Implementing DDD (Vernon)

## When to use

Use when business complexity, model language, lifecycle rules, or cross-team/system boundaries shape design more than generic technical organization.

## Primary bias to correct

Keep domain behavior, code, tests, documents, and team language aligned inside explicit Bounded Contexts. Do not let persistence, UI, frameworks, integration formats, or DDD vocabulary replace an implementation-driving model.

## Decision rules

- Before designing code, identify the business capability, classify the subdomain as Core, Supporting, or Generic, define the Bounded Context, use its Ubiquitous Language, and choose only tactical patterns that earn their cost.
- Maintain one Ubiquitous Language per Bounded Context across names, tests, documents, diagrams, commands, events, APIs, and feature discussion; one concept gets one term, one term must not carry multiple meanings inside a context.
- Put business logic in the domain layer. Keep UI, application coordination, infrastructure, persistence, messaging, and framework constraints outside the model or behind adapters.
- Protect the Core Domain from generic abstractions, vendor terms, and supporting complexity; spend richer modeling where competitive or operational complexity lives, keep supporting subdomains simpler, and avoid DDD ceremony for trivial CRUD.
- Define every Bounded Context explicitly. Do not assume a term has the same meaning elsewhere; use context maps, tests, and active communication to protect model integrity.
- Choose context relationships deliberately: Partnership, Shared Kernel, Customer/Supplier, Conformist, Anticorruption Layer, Open Host Service, Published Language, Separate Ways, or incremental legacy replacement — each implies different ownership and translation duties.
- Translate foreign, legacy, partner, external, and infrastructure models into the local language; keep foreign schemas, statuses, contract models, and aggregates out of local domain objects.
- Use Entities when identity and lifecycle matter; make identity explicit and protect meaningful state transitions rather than exposing unrestricted setters.
- Use immutable, self-validating Value Objects for meaningful descriptive concepts; validate at construction, compare by value, and replace raw primitives for meaningful identifiers, quantities, ranges, names, and whole values.
- Use Aggregates as immediate consistency boundaries: keep them small, expose one root, route invariant-changing behavior through the root, hide mutable internals, reference other Aggregates by identity, and default to one Aggregate per transaction; use events, policies, or process coordination when consistency can be eventual.
- Provide Repositories for Aggregate Roots, not tables; define interfaces by domain or application needs, return domain objects, and keep business rules out of repository implementations.
- Publish Domain Events only for meaningful completed business facts; name them in past tense, keep payloads local to the model, and do not use events for every property change or to hide poor Aggregate design.
- Use Event Sourcing only when the event sequence is the right persistence model; streams must match Aggregate identity and versioning, replay must be deterministic, and event meaning changes need versioning, upcasters, or translators.
- Application Services coordinate use cases by loading Aggregates, invoking domain behavior, persisting results, publishing resulting events, and owning transaction or integration coordination — they must not become the real domain model.
- Organize modules by Bounded Context first and by domain or use-case ownership within the context; avoid giant shared or common packages for domain concepts.
- Refactor toward deeper domain insight, not only mechanical cleanliness. Make constraints, policies, processes, calculations, allocations, and generation rules explicit when they carry domain meaning.
- Keep frameworks, persistence mechanics, transport formats, REST representations, and infrastructure types out of the domain model. Translate external data at the boundary.
- Test domain behavior and boundaries directly: Aggregate invariants, valid and invalid state transitions, Value Object validation, Domain Events as outcomes, repositories as infrastructure, translation layers, and application-service orchestration.

## Trigger rules

- When terminology is awkward, ambiguous, inconsistent, reused across contexts, or repeatedly translated, refine the Ubiquitous Language and rename code before adding more behavior.
- When controllers, services, scripts, SQL, jobs, or serializers carry business decisions, move rules into domain objects, domain services, specifications, or explicit domain concepts.
- When UI, persistence, messaging, APIs, or frameworks start shaping domain concepts, isolate them with layers, adapters, translation, or an Anticorruption Layer.
- When one model spreads across billing, identity, catalog, fulfillment, or other separate concerns, split or translate instead of reusing shared domain classes.
- When a change crosses unrelated modules, many objects, or multiple roots, reassess Module cohesion, Aggregate ownership, consistency timing, and context boundaries.
- When an Aggregate boundary changes or one transaction wants multiple Aggregates, list the immediate invariants that require it; otherwise coordinate by identity, events, policies, or Application Services.
- When code wants to import another context's domain package, share enums across contexts, or couple through another context's database, add explicit translation instead.
- When a Repository becomes generic CRUD, table-shaped, row-returning, or starts enforcing business rules, reshape it around Aggregate access and move rules back to the model.
- When Application Services or controllers accumulate branching business rules, move the decision into the Entity, Value Object, Aggregate, or Domain Service that owns the concept.
- When a Domain Event reads like a command, exposes framework artifacts, or describes a minor property change, rename, narrow, or remove it.
- When delivery pressure tempts the team to skip design, use a short modeling spike, scenario, or acceptance test and record known modeling debt.

## Final checklist

- Domain behavior explicit in the model rather than hidden in delivery, persistence, or integration code?
- Code, tests, documents, and conversations use one language inside each Bounded Context?
- Core Domain visible and protected from supporting complexity, generic mechanisms, and frameworks?
- Every cross-context integration has an explicit relationship, translation strategy, and boundary test?
- Aggregates small, root-protected, invariant-driven, identity-linked, and usually one per transaction?
- Entities behavior-bearing and Value Objects immutable, validated, and value-equal?
- Repositories Aggregate-root access points rather than generic DAOs or ORM leaks?
- Domain Events meaningful past-tense facts?
- Application Services coordinating use cases instead of owning domain decisions?
- Infrastructure, persistence, REST, and transport details kept out of the domain model?
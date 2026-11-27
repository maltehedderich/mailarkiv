# Specification Quality Checklist: Find Messages Quickly and Safely

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-11-15
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details (languages, frameworks, APIs)
- [x] Focused on user value and business needs
- [x] Written for non-technical stakeholders
- [x] All mandatory sections completed

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain
- [x] Requirements are testable and unambiguous
- [x] Success criteria are measurable
- [x] Success criteria are technology-agnostic (no implementation details)
- [x] All acceptance scenarios are defined
- [x] Edge cases are identified
- [x] Scope is clearly bounded
- [x] Dependencies and assumptions identified

## Feature Readiness

- [x] All functional requirements have clear acceptance criteria
- [x] User scenarios cover primary flows
- [x] Feature meets measurable outcomes defined in Success Criteria
- [x] No implementation details leak into specification

## Notes

- Items marked incomplete require spec updates before `/speckit-clarify` or `/speckit-plan`
- Iteration 1: FR-012 holds one marker, which date orders the list and results and drives the date filter. Issue #3 leaves the sort order open. The Date header edge case waits on the same answer.
- The spec names mail and web terms (encoded words, TNEF, S/MIME, prefetch hints, WCAG) because they describe what an administrator sees or what a test must cover. It names no language, library, or storage engine; the engine, parser, and preview design stay in issue #3, ADR 0003, and ADR 0007.
- Fixed in iteration 1: SC-005 killed the search process 20 times, which would trip FR-040's 5-crash threshold. It now kills it at 4 named points. FR-039 now resets the restart wait after 10 minutes of running, so the 70-second bound in SC-005 holds.
- Fixed in iteration 1: embedded images were listed as attachments under FR-031, so signature logos would have set the attachment mark on most business mail. FR-008 now excludes them from the mark and the Has attachments filter.
- Fixed in iteration 1: Assumptions named the search process's toolchain and a specific advisory. They now point to ADR 0007, which holds those details.
- Iteration 2: the user chose the bounded sent date for FR-012. The list date is the Date header unless it is missing, unreadable, or more than 24 hours after the first archive time, which then replaces it. FR-012 adds a fixed tie order for stable pages and replaces Slice 1's newest-import order. FR-023, User Story 1 scenarios 13 and 14, four edge cases, SC-001, and a List date assumption follow from it. All items pass.
- Iteration 3: the user chose to show the sender's address beside the name in rows, because a name alone hides display-name spoofing. FR-008 shows both, and once when they're equal. FR-011 keeps the domain in view and sets name and address apart. User Story 1 scenario 1, a new User Story 3 scenario 6, three edge cases, a Sender in rows assumption, and a sender rule in `frontend/DESIGN.md` follow from it. All items pass.
- Iteration 4: the user chose to show embedded images in the HTML preview and to load images from the internet when an administrator asks, one message view at a time. The Scope, FR-025, FR-026, FR-027, FR-051, User Story 2 with scenarios 4, 8, and 12 changed and 15 to 17 added, four edge cases, SC-002, a new SC-013, the Excluded table, new Embedded images and Images from the internet assumptions, notes in the Decided design and Plan inputs assumptions, and `docs/ARCHITECTURE.md` follow from it. Issue #3's decided design still says any CSP exception must block remote loads; its acceptance criterion, "blocked by default", holds. All items pass.

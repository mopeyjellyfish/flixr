# Release-one completion: accepted scope update

## User decision

The user requested completion of the remaining Flixr plan, a fully polished UI, and a rebuilt demo with its link. They explicitly selected full remaining-plan implementation, not UI-only work.

The user approved `feat/screens` based on `refactor/frontend-ui-architecture` at `ae9870c140f59780ae137b5772766e10bb7a1662`. This supersedes the older `feat/playback` base and stack position in slices 006 and 007 of `plan.md`. Keep both slices in this sequential delivery, followed by UI polish. Proceed without intermediate routine approval checkpoints. Tests, review, bounded commits, and PR publication are authorized. Demo rebuild through `make demo` is authorized after verification.

## Scope and order

1. Implement slice 006: expiring, profile-scoped LAN screen authority; authenticated origin-checked WebSockets; play/pause/seek/handoff; disconnect recovery; owner connected-screen view; bounded shutdown.
2. Implement slice 007: runtime-qualified Google Cast and native AirPlay, fixed receiver planning, short-lived planner-approved external media URLs, sender progress, and trusted-HTTPS prerequisite guidance.
3. Polish existing and added interfaces against `DESIGN.md`, Editorial Stream and Poster Theater. Preserve provider-free setup, semantic actions, readable typography, 44px targets, focus visibility, reduced motion, and intentional phone layout. Correct the visibly overlapping Home rail cards in the current demo. No unrelated visual direction or generic component kit.
4. Freeze, validate, review, publish, and rebuild the existing demo. Report the working link and any unmet device evidence honestly.

## Evidence boundary

Physical Google Cast and Safari/AirPlay acceptance remains unverified until the real devices complete the plan's checklist. API fakes and Playwright WebKit are not physical-device proof. Do not mark all release criteria complete when this evidence is absent. Metadata-only demo titles must remain non-playable, with no Play action.

## Safety and exclusions

Preserve existing demo data and profile credentials. Do not guess credentials, bypass profile authentication, or reset volumes. No merge, release, WAN automation, hosted control plane, deployment beyond the expressly requested local demo rebuild, port `18789` refresh, or unrelated change is authorized. Do not rewrite lower branches. Keep generated browser evidence and embedded assets unpublished.

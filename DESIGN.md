---
version: alpha
name: Cobalt Signal
description: A calm near-black cinema interface where cobalt carries interaction and local-screen readiness stays visible without becoming infrastructure chrome.
colors:
  canvas: "#05070C"
  surface: "#101623"
  interaction: "#5B8CFF"
  text: "#F6F8FF"
  muted: "#AAB4C8"
  success: "#64DFA0"
typography:
  display: Manrope
  interface: Atkinson Hyperlegible Next
omitted:
  - rounded
  - spacing
  - components
---

# Design system

## Overview

Flixr is a couch-first media interface for a household's locally owned films and episodic TV. It should feel premium, modern, calm, and fast. It can use familiar streaming-service information hierarchy, but it must not copy Netflix branding or make server infrastructure the visual subject.

Cobalt Signal is the design authority. Near-black surfaces establish a quiet cinema base. Cobalt identifies interaction, progress, and Flixr itself. White focus treatment remains legible at TV distance. Media artwork supplies most content color.

The same information hierarchy must condense from a ten-foot TV layout to desktop, tablet, and phone. Release one is a responsive React web application. Future native clients may reuse design intent and tokens, but this document does not require identical component implementations across platforms.

Review-only TheTVDB artwork is not product content and must not enter published assets, fixtures, screenshots, or this design system.

## Colors

- **Canvas** is the primary page and player background. Use it to let artwork and controls dominate without visual noise.
- **Surface** separates navigation, overlays, cards, menus, and owner-operation regions from the canvas. Do not create many nearly identical dark surface values without implementation evidence.
- **Interaction** marks selected navigation, progress, active controls, and Flixr identity. Cobalt carries meaning; it is not ambient decoration.
- **Text** is the primary foreground and the default TV-distance focus-ring color.
- **Muted** supports metadata and secondary instructions. It must still meet WCAG 2.2 AA contrast for its text role.
- **Success** communicates local readiness and healthy screen or tool state. Do not use it as a general accent.

Error and warning colors remain unresolved until implementation states provide evidence. Do not infer them from the success or interaction colors.

## Typography

Use self-hosted, open-license Manrope for display headings and Atkinson Hyperlegible Next for body text, metadata, labels, forms, and controls. Provide system fallbacks that preserve readability when a font fails to load.

Display type establishes title hierarchy with strong weight contrast. Interface type favors a large x-height and unambiguous characters. Keep focus labels and primary actions readable at TV distance. Keep descriptive text to a comfortable measure and avoid dense walls of metadata. Titles may wrap; controls and critical status text must not clip.

Exact font sizes, line heights, and weight assignments remain implementation decisions. Validate them at named TV, desktop, tablet, and phone viewports before promoting them to normative tokens.

## Layout

Use a viewing-first hierarchy: profile and navigation, focal title or task, immediate playback actions, local-screen readiness, then browse rails or details. Owner operations remain available but must not dominate the household home surface.

TV layouts prioritize distance, directional navigation, stable focus movement, and visible content context. Desktop and laptop layouts preserve the cinematic hierarchy while making pointer and keyboard operation efficient. Tablet and phone layouts reorder and condense the same information instead of shrinking the TV composition.

Poster rails must not create DOM nodes for the complete catalog. Focal actions must not clip. Controls must not depend on hover. Touch targets are at least 44 CSS pixels where the layout permits.

Exact grids, breakpoints, spacing steps, and container widths remain unresolved until responsive implementation and browser evidence establish them.

## Elevation & Depth

Create depth through restrained contrast between canvas and surface, artwork gradients that protect text, and overlays tied to a functional state. Use blur or shadow only when it clarifies layering, focus, or playback controls.

Do not stack decorative glass panels, glows, or shadows. Cobalt glow is not a substitute for focus, and artwork must not reduce text contrast.

## Shapes

Use coherent, restrained shapes suitable for a premium media tool. Focus outlines must remain outside clipped artwork and be visible against both dark surfaces and colorful posters. Borders may separate adjacent dark regions where contrast alone is insufficient.

Exact radii, border widths, and shape scales remain unresolved until repeated implemented components provide evidence.

## Components

The expected repeated and signature patterns are:

- **Poster rail:** virtualized, directionally navigable, and able to restore focus to the originating item after details close.
- **Focal media region:** title, metadata, concise synopsis, primary playback action, and secondary detail or screen action protected from artwork contrast.
- **Local-screen readiness:** a quiet persistent status near playback actions, with available, connecting, playing, disconnected, and prerequisite-unavailable states.
- **Player controls:** semantic controls for play, pause, seek, resume, stop, handoff, buffering, and actionable playback failure.
- **Profile and PIN flow:** clear owner/profile identity, protected entry, rate-limited failure, and deterministic focus.
- **Owner operations:** scan state, unmatched items, FFmpeg readiness, active playback generations, connected screens, and actionable errors in a distinct owner-only surface.

All controls use native semantic behavior where possible. Keyboard, directional remote, touch, and pointer paths must reach the same actions. Focus is always visible, reduced motion is respected, and focus returns predictably after modal or detail transitions.

Exact component measurements and component-level tokens remain unresolved until implementation evidence supports them.

## Do's and Don'ts

### Do

- Use cobalt for interaction, progress, and identity.
- Use white focus rings that remain visible at TV distance and over artwork.
- Keep local-screen availability close to playback without turning the home page into a device dashboard.
- Show loading, empty, partial-metadata, failure, buffering, disconnected, and unavailable-prerequisite states deliberately.
- Validate TV, desktop, tablet, and phone compositions with representative licensed or original content.
- Preserve semantic controls, WCAG 2.2 AA contrast, reduced motion, and deterministic focus restoration.
- Load player and casting code only when needed.

### Don't

- Do not copy Netflix branding, typography, motion, or signature chrome.
- Do not use cobalt as ambient decoration on every surface.
- Do not make hover the only way to reveal an action.
- Do not shrink a desktop or TV layout unchanged onto a phone.
- Do not expose owner operations in the viewing-first home hierarchy.
- Do not add speculative token scales, component kits, or native-client abstractions before repeated evidence exists.
- Do not publish review-only TheTVDB artwork or use it as product content.

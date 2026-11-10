# ADR-002: Tamagui as the shared UI library (unification step 6 result)

- Status: Accepted (records the spike result for a library choice CJ approved on 2026-10-23)
- Date: 2026-11-10
- Owner: Stockbot Architect
- Evidence: spike PR [#585](https://github.com/Rhionin/stockbot/pull/585), unmerged draft, head `3a5b62ae2e61b6a78b6405dc4a0553897087ba45`

## Context

The web/mobile unification plan, approved by CJ on 2026-10-23, picked Tamagui as the one component library for the web app (React + Vite, Mantine today) and the phone app (Expo). Its ship order made step 6 a spike: prove Tamagui on Expo SDK 58 / React Native 0.88 and in a Vite build of one component before restyling any screens. The plan named the fallback if the spike failed: React Native Web plus the shared token module (option C), not gluestack.

Tamagui's install docs state a React Native 0.81+ floor and do not mention Expo SDK 58. That gap was the main risk the spike had to test. Sources: [Tamagui installation](https://tamagui.dev/docs/intro/installation), [Tamagui Vite guide](https://tamagui.dev/docs/guides/vite), [compiler install](https://tamagui.dev/docs/intro/compiler-install).

## What the spike built

- A new top-level `ui/` package (`@stockbot/ui`). It holds the Tamagui config and one component: the notice banner with one action button.
- `ui/` may import `shared/`. `shared/` stays a pure leaf and never imports `ui/`. The ESLint import boundaries in `frontend/` and `mobile/` enforce this.
- The Tamagui config builds its tokens only from `shared/brandTokens.ts` and the shared notice values. `tokenPalette.ts` maps those values, and `tamagui.config.ts` asserts each one.
- Exact pins: `tamagui` 3.3.1, `@tamagui/vite-plugin` 3.3.1, `@tamagui/cli` 3.3.1, `react-native-web` 0.21.4. Expo ~58.0.22, React Native 0.88.1 and React 19.3.1 are unchanged.
- The harnesses are isolated: a separate Vite entry for the web and a phone screen that no production route registers.

## Results

| Check | Result |
|---|---|
| Phone: real Tamagui renders under jest-expo | Pass. The test renders the real provider and banner, asserts the resolved background, edge, radius and text values against `shared/noticeBanner.ts`, and checks that a press reaches the handler. The only Jest setup file left sets `__DEV__`. |
| Phone: full Jest suite and `tsc` | Pass (builder-reported: 15 suites, 281 tests) |
| Phone: `expo export` Android bundle | Pass. This proves it bundles, not that it renders on a device. |
| Web: Vite production build, Vitest render plus light/dark parity, Playwright plus axe plus keyboard | Pass |
| Web: Tamagui compiler extraction on Vite 8 | Fail. `ReferenceError: module is not defined` at `react/index.js` inside Vite's module runner. No documented fix was found, and the docs prescribe `disableExtraction: true` or the `tamagui build` CLI. |
| Web: runtime cost of the harness entry | About 116.5 KB gzip in total, of which about 53 KB is `tamagui` and `@tamagui/*`. The split is approximate because the `react-native-web` alias folds into Tamagui's output. |
| Official CI on the exact head | Product checks green. One expected failure: "Build and push container image", because the Dockerfile copies `shared/` and `frontend/` but not `ui/`. |

Note on the first phone attempt: an earlier failure (`usePressability is not a function`) came from Jest resolving Tamagui to a bundled `react-native-web-lite` path through a mapper that pointed at a full-package shim. It was not a missing React Native 0.88 API. Config-only mappers fixed it, with no version bumps and no package patches.

## Decision

1. The spike passes for the phone. Tamagui stays the shared UI library, as CJ approved, and step 7 (the shared Tamagui banner and queue card, adopted by the phone) may start.
2. The web runs Tamagui without the compiler for now. The web stays on Mantine until step 8, as the plan already says. Moving web screens onto Tamagui at step 8 is conditional on remeasuring the runtime cost on a real page, or on a supported extraction path for Vite at that time. If the cost is material then, it goes to CJ as a decision.
3. The fallback (option C) is not triggered.

## Conditions on later steps

- **Container build first.** Before any `ui/` code merges to master, a deploy ticket (owner: Network Engineer) updates the Dockerfile to copy `ui/`. Builders do not edit deploy files.
- **Step 7 is phone-only.** The step 7 PR wires Tamagui into `mobile/` and `ui/` only. The web build changes from the spike stay out of master until step 8 gets its own review. Those changes are the Vite plugin, the `react` and `react-native` aliases, and the global `process.env.NODE_ENV` and `__DEV__` defines.
- **Pins stay exact.** Any Tamagui, Expo, React Native or React upgrade needs architecture review, and goes to CJ if it is forced.
- **Device check.** Step 7 adds one on-device render of the banner (a dev client, or a Stockbot Explorer session if Manager schedules one). Jest and `expo export` do not cover the native runtime.
- **Token rule holds.** New Tamagui tokens come only from `shared/brandTokens.ts`. The hex and `rgb(` ratchet applies to `ui/`.
- **#585 is not merged.** It closes once this ADR merges. Step 7 lands `ui/` again through its own reviewed PR.

## Consequences

- Positive: one component library and theme engine across both apps, fed by the shared tokens, with real rendering covered by tests on both sides.
- Negative: the web cannot use the compiler on Vite 8, so web screens would pay the runtime cost (about 53 KB gzip in the spike harness) unless that changes by step 8. Jest needs Tamagui-specific mappers and `transformIgnorePatterns` entries, which upgrades must keep working.
- Opinion: the phone result is the one that mattered for step 7. The web compiler gap is a step 8 question, and stopping after step 7 is still a coherent halfway point.

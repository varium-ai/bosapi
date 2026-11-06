# Frontend conventions

This directory contains the React and TypeScript console. Use pnpm to keep
`pnpm-lock.yaml` consistent with the Docker build. The build uses pnpm 9.15.9;
Node.js 22 is a suitable local development version.

## Commands

```sh
pnpm install --frozen-lockfile
pnpm run dev
pnpm run check
pnpm run build
```

`check` runs ESLint, Prettier verification, TypeScript, and all frontend regression
tests. The API client and resource-name tests start temporary loopback servers.
They require permission to listen on `127.0.0.1` and do not contact a business API.

Use `pnpm run format` to apply formatting and `pnpm run lint:fix` to apply safe lint
fixes. Review the diff after either command. `pnpm run typecheck` checks types
without generating a production build. The existing individual `test:*` commands
remain available for focused checks.

## TypeScript and React

- Use two spaces, single quotes, semicolons, and trailing commas. Prettier owns
  formatting; ESLint owns code checks. Do not add competing formatting rules.
- Mark type imports with `import type` or inline `type` specifiers. ESLint enforces
  this convention and checks React JSX and hook call order.
- Prefer named function components with explicit props for new components. Retain
  `forwardRef` where callers depend on a ref. Existing `React.FC` declarations can
  be retained when changing them would add unrelated work.
- Give API responses, list rows, and form values concrete types. Shared response
  envelopes and pagination types live in `src/api/types.ts`; feature-specific types
  live beside their feature. Type annotations describe the server contract and do
  not replace runtime validation.
- The existing permissive `any` rule remains at legacy API boundaries. Avoid
  introducing new `any` where a concrete type or `unknown` is appropriate.
- Keep the current loading, cancellation, query keys, role checks, form validation,
  and global API error handling when making presentation changes. Changing hook
  dependencies or replacing a page's data-loading approach needs its own review.

## Presentation and shared components

- Reuse `ConsoleToolbar`, `SearchInput`, `TableAction`, `StatusTag`, `LoadError`,
  `AdvancedFields`, and `useDiscardChanges` for their existing console interactions.
  Searchable selects share `SelectLoadError` so retry actions preserve focus.
- Put fixed layout rules in scoped CSS beside the component. Retain inline styles
  for runtime values, responsive measurements, and Ant Design theme tokens.
- Use the semantic CSS variables in `src/style.css` and the Ant Design palette in
  `src/theme/palette.ts`. `tests/theme.test.cjs` checks that their shared colors
  remain synchronized. Keep success, warning, and error feedback distinguishable.
- Keep the global stylesheet import order in `src/main.tsx`. Avoid new global
  selectors and `!important` overrides for feature-specific layouts.
- Support Simplified Chinese, English, Japanese, German, and Spanish. Preserve
  translation keys and placeholders across all five catalogs. Navigation and UI search follow the selected language. Public help articles
  and integration recipes use the canonical English documentation. Put product
  copy in localization resources and developer-facing explanations in English.

## Verification

Run `pnpm run check` and `pnpm run build` before handing off a change. For changes to
controls or layout, also check the relevant page at desktop and narrow widths.
`pnpm run preview:ui` provides isolated local fixtures; it does not connect to a
business backend. `preview:connected` uses the configured backend and should only
be used when that access is intended.

Some older regression checks inspect source code as well as executing helpers.
Keep those assertions tolerant of whitespace changes while retaining the same
checks for endpoints, permissions, secret handling, and mounted form fields.

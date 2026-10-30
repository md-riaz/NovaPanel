# Web shell

Stateless chi + html/template renderer with self-hosted HTMX and compiled Tailwind CSS, following the canonical stack decision in docs/spec/05-adr.md. The agent remains the session, authorization and persistence authority. The web process only uses the existing RPC client.

## Blocked login

`POST /login` returns 503 without making an RPC call. PR #75 comment 6521129631 requests the missing canonical anonymous-session CSRF retrieval path. Do not replace this with anonymous whoami, a second session.start, a CSRF cookie/cache, or a new RPC field. This slice is not complete Phase 2 acceptance.

## Checks

- `go test ./internal/web ./cmd/panel-web`
- `go test -race ./internal/web ./cmd/panel-web`
- `go vet ./internal/web ./cmd/panel-web`
- Install jsdom 26.1.0 and jsqr 1.4.0 in a scratch npm prefix, then run `NODE_PATH=/path/to/scratch/node_modules node internal/web/assets_test.cjs`. This tests the CSRF event and independently decodes the rendered enrollment SVG with jsQR. These are test-only dependencies, not served assets.

The module owner supplies github.com/go-chi/chi/v5 v5.5.0. Until that dependency commit is integrated, checks require an isolated scratch `-modfile` containing chi. No module files belong to this slice.

## Assets

- `assets/htmx.min.js` is self-hosted HTMX. Its license is `assets/htmx.LICENSE`.
- `assets/qrcode.js` is Kazuhiko Arase's MIT-licensed QR Code Generator, with the upstream copyright/license notice retained. Enrollment uses its byte-mode encoder and a local DOM-created SVG, without a remote QR service.
- `styles/input.css` is the Tailwind 4.1.18 build input. Rebuild using scratch-installed `@tailwindcss/cli` 4.1.18 with this directory as the project source, resolving `tailwindcss` from that scratch installation, and emit `assets/app.css`. No CDN or Node runtime is needed by panel-web.

Browser acceptance, the real auth-agent flow, anonymous login and final privileged VM acceptance remain unverified. Dashboard metrics are read-only placeholders until their later-phase agent verbs exist.

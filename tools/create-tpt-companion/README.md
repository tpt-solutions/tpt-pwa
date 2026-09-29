# create-tpt-companion

Scaffolds a new tpt-cortex companion package from a template — with copyright headers, manifest license fields (`MIT OR Apache-2.0`), starter code that mirrors the contract's ping shape, a starter test, and per-language ignores/manifests — so a new companion starts green.

## Usage

From anywhere in the repo (defaults to the current directory as target root):

```sh
node tools/create-tpt-companion --name cortex-weather --lang go \
    --description "Fetches weather for the PWA natively"
```

| Flag | Meaning |
| --- | --- |
| `--name` (required) | kebab-case package name (`cortex-…` recommended) |
| `--lang` (required) | `go` · `rust` · `ts` |
| `--description` | one-liner for the manifest + README |
| `--dir` | target root (defaults to the current directory) |
| `--dry-run` | print the file plan and exit without writing |
| `--force` | allow writing into a target directory that already exists |

TS companions are registered into a `pnpm-workspace.yaml` at the target root automatically (skipped when already listed; the tool fails with the manual edit to make when the packages list can't be parsed); run `pnpm install` afterwards.

## What you get

| Language | Files |
| --- | --- |
| `go` | `go.mod` (module path under `github.com/tpt-solutions/tpt-pwa/…`), `§name§.go`, `§name§_test.go`, README |
| `rust` | `Cargo.toml` (`license = "MIT OR Apache-2.0"`), `src/lib.rs` with test, `.gitignore`, README |
| `ts` | `package.json` (license field, check/test scripts), `src/index.ts`, `src/index.test.ts`, strict `tsconfig.json`, `.gitignore`, README |

Every generated starter compiles and its test passes out of the box (verified for Go and Rust against the repo toolchains).

## Conventions the templates bake in

- Source-file copyright headers (`Copyright … TPT Solutions. Dual-licensed MIT OR Apache-2.0.`) — see CONTRIBUTING.md.
- Manifest license fields for every ecosystem that supports them.
- The starter exposes a `status()`/`Status` shaped like the contract's `cortex.ping` result, because companions surface their capabilities over the daemon's JSON-RPC contract (docs/jsonrpc-contract.md).

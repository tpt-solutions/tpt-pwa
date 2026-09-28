#!/usr/bin/env node
// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
// create-tpt-companion: scaffold a new tpt-cortex companion package from a
// template, with the license headers, manifest license fields, starter code,
// and tests already in place. Run from the repo root:
//
//   node tools/create-tpt-companion --name cortex-weather --lang go \
//       --description "Fetches weather for the PWA natively"
//
// See tools/create-tpt-companion/README.md for details.
import { existsSync, readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'

const YEAR = new Date().getFullYear()
const LICENSE_LINE = `Copyright ${YEAR} TPT Solutions. Dual-licensed MIT OR Apache-2.0.`

function parseArgs(argv) {
  const args = { dir: process.cwd() }
  for (let i = 0; i < argv.length; i++) {
    const flag = argv[i]
    if (flag === '--name') args.name = argv[++i]
    else if (flag === '--lang') args.lang = argv[++i]
    else if (flag === '--description') args.description = argv[++i]
    else if (flag === '--dir') args.dir = resolve(argv[++i])
    else if (flag === '--help' || flag === '-h') args.help = true
    else {
      console.error(`create-tpt-companion: unknown argument ${flag}`)
      process.exit(2)
    }
  }
  return args
}

function fail(message) {
  console.error(`create-tpt-companion: ${message}`)
  process.exit(2)
}

// ---- templates ------------------------------------------------------------

function goMod(name) {
  return `// ${LICENSE_LINE}
module github.com/tpt-solutions/tpt-pwa/${name}

go 1.25
`
}

function goSource(name, description) {
  return `// ${LICENSE_LINE}

// Package main is the ${name} companion (${description || 'describe me'}).
// Companion packages expose their capabilities to the PWA over the daemon's
// JSON-RPC contract -- see docs/jsonrpc-contract.md.
package main

import "fmt"

// Status is the companion's reported health, mirroring cortex.ping's shape.
type Status struct {
	Ready   bool
	Version string
}

func status() Status {
	return Status{Ready: true, Version: "0.1.0"}
}

func main() {
	fmt.Printf("%s: %+v\\n", "${name}", status())
}
`
}

function goTest(name) {
  return `// ${LICENSE_LINE}
package main

import "testing"

func TestStatusIsReady(t *testing.T) {
	status := status()
	if !status.Ready {
		t.Fatal("fresh companion must report ready")
	}
	if status.Version != "0.1.0" {
		t.Fatalf("unexpected version %q", status.Version)
	}
}
`
}

function cargoToml(name, description) {
  return `# ${LICENSE_LINE}
[package]
name = "${name}"
version = "0.1.0"
edition = "2021"
license = "MIT OR Apache-2.0"
description = "${description || 'tpt-cortex companion'}"
repository = "https://github.com/tpt-solutions/tpt-pwa"
`
}

function rustLib(name, description) {
  return `// ${LICENSE_LINE}

//! ${name}: ${description || 'tpt-cortex companion'}.
//!
//! Companion crates expose their capabilities to the PWA through the
//! daemon's JSON-RPC contract -- see docs/jsonrpc-contract.md. Keep the core
//! pure: effects belong behind a trait so the safety argument stays small.

/// Report this companion's identity, mirroring the contract's ping shape.
pub fn status() -> (bool, &'static str) {
    (true, "0.1.0")
}

#[cfg(test)]
mod tests {
    #[test]
    fn fresh_companion_is_ready() {
        let (ready, version) = super::status();
        assert!(ready);
        assert_eq!(version, "0.1.0");
    }
}
`
}

function packageJson(name, description) {
  return JSON.stringify(
    {
      name,
      private: true,
      version: '0.1.0',
      type: 'module',
      license: 'MIT OR Apache-2.0',
      description: description || 'tpt-cortex companion',
      scripts: {
        check: 'tsc --noEmit',
        test: 'vitest run',
      },
    },
    null,
    2,
  ) + '\n'
}

function tsSource(description) {
  return `// ${LICENSE_LINE}

/**
 * tpt-cortex companion (${description || 'describe me'}).
 *
 * Companion modules talk to the daemon over the JSON-RPC contract
 * (docs/jsonrpc-contract.md) and degrade gracefully when it is absent.
 */
export function status(): { ready: boolean; version: string } {
  return { ready: true, version: '0.1.0' }
}
`
}

function tsTest() {
  return `// ${LICENSE_LINE}
import { expect, it } from 'vitest'
import { status } from './index'

it('reports a fresh companion as ready', () => {
  expect(status()).toEqual({ ready: true, version: '0.1.0' })
})
`
}

function readme(name, lang, description) {
  const commands =
    lang === 'go'
      ? '```sh\ngo vet ./... && go build ./... && go test ./...\n```'
      : lang === 'rust'
        ? '```sh\ncargo fmt --check && cargo clippy -- -D warnings && cargo test\n```'
        : '```sh\npnpm install && pnpm run check && pnpm run test\n```'
  const workspaceNote =
    lang === 'ts'
      ? `Register the package in the root \`pnpm-workspace.yaml\` packages list (\`- "${name}/"\`), then run \`pnpm install\` at the repo root.\n\n`
      : ''
  return `# ${name}

${description || 'A tpt-cortex companion package.'} Part of the tpt-pwa monorepo.

## Development

${commands}

${workspaceNote}## License

Dual-licensed MIT OR Apache-2.0, like the rest of the monorepo — see the root LICENSE-MIT / LICENSE-APACHE files.
`
}

function gitignore(lang) {
  const common = '.DS_Store\nThumbs.db\n.env\n.env.*\n!.env.example\n'
  if (lang === 'go') return common
  if (lang === 'rust') return `${common}target/\n`
  return `${common}node_modules/\ndist/\n*.tsbuildinfo\n.vite/\ncoverage/\n`
}

// ---- generation -----------------------------------------------------------

function main() {
  const args = parseArgs(process.argv.slice(2))
  if (args.help) {
    console.log(`usage: node tools/create-tpt-companion --name <kebab-name> --lang <go|rust|ts> [--description "..."] [--dir <target>]`)
    process.exit(0)
  }
  if (!args.name || !/^[a-z][a-z0-9-]*$/.test(args.name)) {
    fail('--name is required and must be kebab-case (letters, digits, dashes)')
  }
  if (!['go', 'rust', 'ts'].includes(args.lang)) {
    fail('--lang must be one of: go, rust, ts')
  }
  const target = join(args.dir, args.name)
  if (existsSync(target)) {
    fail(`${target} already exists; refusing to overwrite`)
  }

  const description = args.description ?? ''
  const files = new Map()

  if (args.lang === 'go') {
    files.set('go.mod', goMod(args.name))
    files.set(`${args.name}.go`, goSource(args.name, description))
    files.set(`${args.name}_test.go`, goTest(args.name))
  } else if (args.lang === 'rust') {
    files.set('Cargo.toml', cargoToml(args.name, description))
    files.set('src/lib.rs', rustLib(args.name, description))
    files.set('.gitignore', gitignore('rust'))
  } else {
    files.set('package.json', packageJson(args.name, description))
    files.set('src/index.ts', tsSource(description))
    files.set('src/index.test.ts', tsTest())
    files.set('tsconfig.json', JSON.stringify({ compilerOptions: { strict: true, target: 'es2022', module: 'esnext', moduleResolution: 'bundler', noEmit: true }, include: ['src'] }, null, 2) + '\n')
    files.set('.gitignore', gitignore('ts'))
  }
  files.set('README.md', readme(args.name, args.lang, description))

  for (const [relative, content] of files) {
    const absolute = join(target, relative)
    mkdirSync(dirname(absolute), { recursive: true })
    writeFileSync(absolute, content)
  }

  // Convenience: register TS companions with a pnpm workspace at the target
  // root, when one exists and doesn't already list the package.
  if (args.lang === 'ts') {
    const workspacePath = join(args.dir, 'pnpm-workspace.yaml')
    if (existsSync(workspacePath)) {
      const workspace = readFileSync(workspacePath, 'utf8')
      if (!workspace.includes(`"${args.name}/"`)) {
        const updated = workspace.replace(
          /packages:\s*\n((?:\s*-\s*"[^"]+"\n?)*)/,
          (m, list) => `packages:\n${list.endsWith('\n') ? list : list + '\n'}  - "${args.name}/"\n`,
        )
        writeFileSync(workspacePath, updated)
        console.log(`create-tpt-companion: added "${args.name}/" to pnpm-workspace.yaml`)
      }
    }
  }

  console.log(`create-tpt-companion: created ${args.lang} companion at ${target}`)
  for (const relative of files.keys()) console.log(`  ${relative}`)
  console.log('next steps:')
  if (args.lang === 'go') console.log(`  cd ${args.name} && go vet ./... && go test ./...`)
  else if (args.lang === 'rust') console.log(`  cd ${args.name} && cargo test`)
  else console.log(`  pnpm install (registers the new workspace member), then: pnpm --filter ${args.name} test`)
}

main()

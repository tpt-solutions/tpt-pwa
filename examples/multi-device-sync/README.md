# Example: multi-device sync via the CRDT (no central arbitrator)

Demonstrates the conflict-resolution scaffold from spec §5: automerge-rs (Wasm) holds the note set, and merging is commutative/associative/idempotent — so devices that edited the same data offline converge to one identical state without a server deciding who wins.

## The scenario (executable)

[pwa/src/lib/crdt-multi-device.test.ts](../../pwa/src/lib/crdt-multi-device.test.ts) is the demo — run it with:

```sh
pnpm --filter tpt-pwa test
```

It walks through the realistic flow:

1. **Shared origin** — one document binary is shipped to two devices.
2. **Divergence** — both go offline and edit the *same* note: device A changes the title, device B the body.
3. **Convergence** — connectivity returns; each device merges the other's binary. Both end with A's title AND B's body — neither write is lost, in either merge order.
4. **Idempotence** — re-merging changes nothing.
5. **Add-only case** — a note created offline on one device simply appears on the other.

## How it fits the system today

- Local persistence stays in SQLite/IndexedDB (`NoteStorage`); the `NoteDoc` (`pwa/src/lib/crdt.ts`) is a parallel mirror, updated best-effort on every write.
- `capabilities.crdt` shows whether the Wasm module loaded; absence disables merging only.

## How it becomes multi-device

The daemon will ferry these opaque binaries between a user's devices — a `cortex.task` of kind `crdtMerge` whose payload is the saved document; the receiving PWA applies `NoteDoc.merge(binary)` on receipt and refreshes. No server-side logic is needed beyond transport: the merge semantics live entirely in the CRDT. The transport wiring is the same `cortex.task.enqueue` / `cortex.event.taskCompleted` loop demonstrated in [examples/local-sync](../local-sync/README.md).

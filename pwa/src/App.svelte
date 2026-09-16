<script lang="ts">
  import { onMount } from 'svelte'
  import { installPrompt, appStatus, capabilities, notes, online, pendingSync } from './lib/stores'
  import { createNote, deleteNote, updateNote } from './lib/app'
  import type { Note } from './lib/storage'

  type View = { name: 'list' } | { name: 'editor'; id: string }

  let view = $state<View>({ name: 'list' })

  function findSelected(notesList: Note[], current: View): Note | null {
    if (current.name !== 'editor') return null
    return notesList.find((note) => note.id === current.id) ?? null
  }

  const selected = $derived(findSelected($notes, view))
  const backendLabel = $derived(
    { sqlite: 'SQLite · OPFS', indexeddb: 'IndexedDB', memory: 'In-memory (session)' }[
      $capabilities.storageBackend ?? 'memory'
    ],
  )

  const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })
  const formatWhen = (ts: number) => dateFormat.format(new Date(ts))

  /** Route view swaps through the View Transitions API when available (spec §3 Layer 1). */
  function transition(update: () => void): void {
    if (typeof document.startViewTransition === 'function') {
      void document.startViewTransition(() => update()).finished.catch(() => {})
    } else {
      update()
    }
  }

  function openEditor(note: Note): void {
    transition(() => {
      view = { name: 'editor', id: note.id }
    })
  }

  function closeEditor(): void {
    transition(() => {
      view = { name: 'list' }
    })
  }

  async function newNote(): Promise<void> {
    const note = await createNote('', '')
    transition(() => {
      view = { name: 'editor', id: note.id }
    })
  }

  async function removeCurrent(): Promise<void> {
    if (!selected) return
    const { id } = selected
    transition(() => {
      view = { name: 'list' }
    })
    await deleteNote(id)
  }

  onMount(() => {
    const handler = (event: Event) => {
      event.preventDefault()
      const promptable = event as Event & { prompt?: () => Promise<void> }
      if (typeof promptable.prompt === 'function') {
        installPrompt.set({ prompt: () => promptable.prompt!() })
      }
    }
    window.addEventListener('beforeinstallprompt', handler)
    return () => window.removeEventListener('beforeinstallprompt', handler)
  })
</script>

<div class="app">
  <header class="app-header">
    <div class="brand">
      <svg class="brand-mark" viewBox="0 0 64 64" aria-hidden="true">
        <rect width="64" height="64" rx="14" fill="#0f1115"></rect>
        <path d="M18 17h28v9H36.5v21h-9V26H18z" fill="#6ea8ff"></path>
      </svg>
      <span class="brand-name">tpt-pwa</span>
    </div>
    <div class="header-status">
      {#if !$online}
        <span class="chip chip--warn" title="Changes queue locally until connectivity returns">offline</span>
      {/if}
      {#if $pendingSync > 0}
        <span class="chip chip--accent" title="Entries in the durable outbox">{$pendingSync} queued</span>
      {/if}
      <span
        class="chip"
        class:chip--accent={$capabilities.cortex}
        title={$capabilities.cortex
          ? 'tpt-cortex daemon reachable on ws://127.0.0.1:9911 — background tasks run natively'
          : 'No daemon found — using Service Worker + local storage fallbacks'}
      >
        {$capabilities.cortex ? 'cortex connected' : 'fallback mode'}
      </span>
      {#if $installPrompt}
        <button class="button button--small" onclick={() => $installPrompt?.prompt()}>Install</button>
      {/if}
    </div>
  </header>

  <main class="app-main">
    {#if $appStatus === 'loading'}
      <p class="muted pad">Negotiating capabilities…</p>
    {:else if $appStatus === 'error'}
      <section class="card pad">
        <h1>Storage unavailable</h1>
        <p class="muted">Local persistence failed to initialise. Reload to retry capability negotiation.</p>
      </section>
    {:else if view.name === 'list'}
      <section class="list-view">
        <div class="list-toolbar">
          <h1>Notes</h1>
          <button class="button" onclick={() => void newNote()}>+ New note</button>
        </div>
        {#if $notes.length === 0}
          <div class="card pad empty">
            <h2>No notes yet</h2>
            <p class="muted">
              Create one offline — it persists in {$capabilities.storageBackend === 'sqlite'
                ? 'SQLite on the Origin Private File System'
                : 'local storage'} and syncs when a path is available.
            </p>
            <button class="button" onclick={() => void newNote()}>Create your first note</button>
          </div>
        {:else}
          <ul class="note-list">
            {#each $notes as note (note.id)}
              <li>
                <button class="note-card" onclick={() => openEditor(note)}>
                  <span class="note-title">{note.title === '' ? 'Untitled' : note.title}</span>
                  <span class="note-body">{note.body === '' ? 'No content yet' : note.body}</span>
                  <span class="note-meta">
                    {formatWhen(note.updatedAt)}
                    {#if note.syncedAt === null}<span class="dot dot--pending" title="Waiting to sync"></span>{/if}
                  </span>
                </button>
              </li>
            {/each}
          </ul>
        {/if}
      </section>
    {:else if selected}
      <section class="editor">
        <div class="editor-toolbar">
          <button class="button button--ghost" onclick={closeEditor}>← Notes</button>
          <button class="button button--danger" onclick={() => void removeCurrent()}>Delete</button>
        </div>
        <input
          class="editor-title"
          placeholder="Title"
          value={selected.title}
          oninput={(event) => {
            const current = selected
            if (current) updateNote(current.id, { title: event.currentTarget.value })
          }}
        />
        <textarea
          class="editor-body"
          placeholder="Start writing — everything is saved locally as you type."
          value={selected.body}
          oninput={(event) => {
            const current = selected
            if (current) updateNote(current.id, { body: event.currentTarget.value })
          }}
        ></textarea>
      </section>
    {:else}
      <section class="card pad">
        <h1>Note not found</h1>
        <p class="muted">It may have been deleted on another device.</p>
        <button class="button" onclick={closeEditor}>← Back to notes</button>
      </section>
    {/if}
  </main>

  <footer class="app-footer">
    <span>storage: {backendLabel}</span>
    <span>·</span>
    <span>crdt: {$capabilities.crdt ? 'ready' : 'unavailable'}</span>
    <span>·</span>
    <span>offline-first, app-store-free</span>
  </footer>
</div>

<script lang="ts">
  // Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
  import { onMount } from 'svelte'
  import {
    describeRunOutcome,
    formatValue,
    loadEngine,
    type EnginePlayground,
    type RunReport,
  } from './engine'
  import { saveScript, deleteScript } from './app'
  import { cryptoStatus, scripts as scriptLibrary, type StoredScript } from './stores'

  /**
   * The `.ctx` playground: the real engine (same bytecode as the daemon)
   * compiled to WebAssembly, running against a deterministic sandbox.
   * Scripts get fake rows + a connectivity flag; `http.post`/`db.exec`
   * effects are recorded, never fired.
   */

  const DEFAULT_SCRIPT = `task t() -> void {
    let rows = native.db.query("SELECT id, title FROM notes");
    if native.net.isConnected() {
        for row in rows {
            native.http.post("https://api.example/sync", row);
        }
    }
    let count = 0;
    for row in rows {
        count = count + 1;
    }
    return count;
}`

  let ready = $state(false)
  let failed = $state('')
  let source = $state(DEFAULT_SCRIPT)
  let connected = $state(true)
  let rowsJSON = $state('[{"id": "41", "title": "First note"}]')
  let report = $state<RunReport | null>(null)
  let natives = $state<string[]>([])
  let posts = $state<Array<[string, unknown]>>([])
  let statements = $state<Array<[string, unknown[]]>>([])
  let runCount = $state(0)

  let playground: EnginePlayground | null = null
  /** Script library state: the name field, and the entry being re-saved. */
  let libraryName = $state('')
  let editingId = $state<string | null>(null)
  let libraryError = $state('')

  const libraryDateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })

  async function saveToLibrary(): Promise<void> {
    libraryError = ''
    const name = libraryName.trim() === '' ? `script ${libraryDateFormat.format(new Date())}` : libraryName.trim()
    try {
      await saveScript(name, source, editingId ?? undefined)
      libraryName = ''
      editingId = null
    } catch (error) {
      libraryError = error instanceof Error ? error.message : String(error)
    }
  }

  function loadFromLibrary(entry: StoredScript): void {
    source = entry.source
    libraryName = entry.name
    editingId = entry.id
  }

  function startNewScript(): void {
    source = DEFAULT_SCRIPT
    libraryName = ''
    editingId = null
  }

  async function removeFromLibrary(id: string): Promise<void> {
    libraryError = ''
    try {
      await deleteScript(id)
      if (editingId === id) editingId = null
    } catch (error) {
      libraryError = error instanceof Error ? error.message : String(error)
    }
  }

  onMount(async () => {
    try {
      const engine = await loadEngine()
      playground = new engine.Playground()
      ready = true
    } catch (error) {
      failed = error instanceof Error ? error.message : String(error)
    }
  })

  function run(): void {
    if (!playground) return
    playground.set_connected(connected)
    try {
      playground.set_rows(rowsJSON)
    } catch {
      // Invalid rows JSON: run against the last valid rows rather than
      // blocking the script; the field is right above the output.
    }
    report = JSON.parse(playground.run(source)) as RunReport
    posts = JSON.parse(playground.posts())
    statements = JSON.parse(playground.executed_sql())
    try {
      natives = JSON.parse(playground.manifest(source))
    } catch {
      // A script that does not compile has no manifest; the run report
      // already carries that error.
      natives = []
    }
    runCount += 1
  }
</script>

<section class="playground">
  <div class="playground-head">
    <h1>.ctx playground</h1>
    <p class="muted">
      The real cortex-engine VM compiled to WebAssembly — the same bytecode the daemon
      runs. Effects land in the sandbox below, never on your machine.
    </p>
  </div>

  {#if failed}
    <div class="card pad">
      <h2>Engine unavailable</h2>
      <p class="muted">{failed}</p>
      <p class="muted">Rebuild it with <code>pnpm run engine:build</code> (needs Rust + wasm-pack).</p>
    </div>
  {:else if !ready}
    <p class="muted pad">Loading the engine…</p>
  {:else}
    <div class="playground-controls">
      <label class="playground-toggle">
        <input type="checkbox" bind:checked={connected} />
        native.net.isConnected()
      </label>
      <label class="playground-rows">
        <span class="muted">db.query rows (JSON)</span>
        <input class="search-input" type="text" bind:value={rowsJSON} aria-label="Fake database rows as JSON" />
      </label>
      <button class="button" onclick={run}>Run</button>
    </div>

    <textarea
      class="playground-editor"
      spellcheck="false"
      aria-label="Script source"
      bind:value={source}
    ></textarea>

    {#if $cryptoStatus.enabled}
      <p class="muted playground-natives">
        The script library needs the CRDT mirror, which stays off while note encryption is on.
      </p>
    {:else}
      <div class="script-library">
        <div class="crypto-form">
          <input
            class="crypto-input library-name"
            type="text"
            placeholder={editingId ? 'Name (updating)' : 'Script name'}
            aria-label="Script name"
            bind:value={libraryName}
          />
          <button class="button button--small" onclick={() => void saveToLibrary()}>
            {editingId ? 'Update in library' : 'Save to library'}
          </button>
          {#if editingId}
            <button class="button button--small button--ghost" onclick={startNewScript}>New script</button>
          {/if}
        </div>
        {#if libraryError}<p class="playground-error" role="alert">{libraryError}</p>{/if}
        {#if $scriptLibrary.length > 0}
          <ul class="library-list">
            {#each $scriptLibrary as entry (entry.id)}
              <li>
                <button class="button button--small" onclick={() => loadFromLibrary(entry)} title="Load into the editor">
                  {entry.name}
                </button>
                <span class="muted">{libraryDateFormat.format(new Date(entry.updatedAt))}</span>
                <button class="button button--small button--danger" onclick={() => void removeFromLibrary(entry.id)} aria-label={'Delete ' + entry.name}>
                  Delete
                </button>
              </li>
            {/each}
          </ul>
        {:else if !editingId}
          <p class="muted playground-natives">Nothing saved yet — name it and save to keep it across reloads and devices.</p>
        {/if}
      </div>
    {/if}

    <div class="playground-output">
      <div class="playground-result" role="status" aria-live="polite">
        {#if report}
          <span class="playground-result-label">{runCount > 1 ? `run #${runCount}:` : 'result:'}</span>
          {#if report.ok}
            <code class="ok">{formatValue(report.result)}</code>
          {:else}
            <code class="playground-error">{describeRunOutcome(report)}</code>
          {/if}
        {:else}
          <span class="muted">Press Run to execute.</span>
        {/if}
      </div>

      {#if natives.length > 0}
        <p class="muted playground-natives">natives used: {natives.join(', ')}</p>
      {/if}

      {#if statements.length > 0}
        <div class="playground-effects">
          <h2>db.exec</h2>
          <ul>
            {#each statements as [sql, params]}
              <li>
                <code>{sql}</code>
                {#if params.length > 0}<span class="muted">— {JSON.stringify(params)}</span>{/if}
              </li>
            {/each}
          </ul>
        </div>
      {/if}

      {#if posts.length > 0}
        <div class="playground-effects">
          <h2>http.post</h2>
          <ul>
            {#each posts as [url, body]}
              <li><code>{url}</code> <span class="muted">{JSON.stringify(body)}</span></li>
            {/each}
          </ul>
        </div>
      {/if}
    </div>
  {/if}
</section>

// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { normalizeNote, type Note } from './storage'

/**
 * Note export/import. JSON is the fidelity-preserving round-trip format
 * (ids, timestamps); Markdown is the human-interoperable format (viewable
 * in any editor, importable back). Round-tripped Markdown notes carry a
 * `<!-- tpt-pwa-note -->` marker comment holding their metadata -- invisible
 * in rendered Markdown, robust to parse (the marker carries the id, so a
 * body containing `# ` headings cannot split a note).
 */

const MARKER_RE = /<!--\s*tpt-pwa-note\s+([^>]*?)-->/

export type ExportPayload = {
  format: 'tpt-pwa/notes'
  version: 1
  exportedAt: string
  notes: Note[]
}

/** Serialize live notes as pretty JSON. */
export function exportNotesJson(notes: Note[], exportedAt = new Date()): string {
  const payload: ExportPayload = { format: 'tpt-pwa/notes', version: 1, exportedAt: exportedAt.toISOString(), notes }
  return JSON.stringify(payload, null, 2) + '\n'
}

/** Serialize notes as one Markdown document; each note is a `# Title` section with a metadata marker. */
export function exportNotesMarkdown(notes: Note[], exportedAt = new Date()): string {
  const parts: string[] = [`<!-- exported by tpt-pwa ${exportedAt.toISOString()} -->`]
  for (const note of notes) {
    const marker = `<!-- tpt-pwa-note id="${escapeAttr(note.id)}" createdAt="${note.createdAt}" updatedAt="${note.updatedAt}" -->`
    const title = note.title.trim() === '' ? 'Untitled' : note.title
    parts.push(`${marker}\n\n# ${title}\n\n${note.body.trimEnd()}`.trimEnd())
  }
  return parts.join('\n\n') + '\n'
}

function escapeAttr(value: string): string {
  return value.replace(/&/g, '&amp;').replace(/"/g, '&quot;')
}

function unescapeAttr(value: string): string {
  return value.replace(/&quot;/g, '"').replace(/&amp;/g, '&')
}

/** Parse marker attributes (`key="value"` pairs) out of a marker body. */
function parseMarkerAttrs(markerBody: string): Record<string, string> {
  const attrs: Record<string, string> = {}
  for (const match of markerBody.matchAll(/(\w+)="([^"]*)"/g)) attrs[match[1]] = match[2]
  return attrs
}

/** Parse a JSON export back into notes; tolerates a bare note array too. Throws with a friendly message. */
export function parseNotesJson(text: string): Note[] {
  let data: unknown
  try {
    data = JSON.parse(text)
  } catch {
    throw new Error('not valid JSON')
  }
  const rawNotes =
    Array.isArray(data)
      ? data
      : isExportPayload(data)
        ? data.notes
        : null
  if (!Array.isArray(rawNotes)) throw new Error('expected a tpt-pwa notes export ({ format: "tpt-pwa/notes", notes: [...] }) or a note array')
  return rawNotes.map((raw) => normalizeNote(raw as Note))
}

function isExportPayload(data: unknown): data is ExportPayload {
  return typeof data === 'object' && data !== null && (data as ExportPayload).format === 'tpt-pwa/notes' && Array.isArray((data as ExportPayload).notes)
}

/**
 * Parse a Markdown document into notes. Sections written by
 * `exportNotesMarkdown` round-trip with ids and timestamps intact; a
 * document without tpt-pwa markers (any foreign .md file) imports as a
 * single note titled from its first `# ` heading.
 */
export function parseNotesMarkdown(text: string, newId: () => string = () => crypto.randomUUID()): Note[] {
  const markerMatches = [...text.matchAll(new RegExp(MARKER_RE.source, 'g'))]
  if (markerMatches.length === 0) return [foreignNote(text, newId)]

  const notes: Note[] = []
  for (let i = 0; i < markerMatches.length; i++) {
    const match = markerMatches[i]
    const start = match.index! + match[0].length
    const end = i + 1 < markerMatches.length ? markerMatches[i + 1].index! : text.length
    const attrs = parseMarkerAttrs(match[1])
    const section = text.slice(start, end)

    // The title is the first `# ` heading of the section, if present.
    const heading = section.match(/^\s*#\s+(.+?)\s*$/m)
    const title = heading ? heading[1] : ''
    const body = heading ? section.slice(section.indexOf(heading[0]) + heading[0].length) : section

    notes.push({
      id: attrs.id ? unescapeAttr(attrs.id) : newId(),
      title: title === 'Untitled' ? '' : title.trim(),
      body: body.trim(),
      createdAt: Number(attrs.createdAt) || Date.now(),
      updatedAt: Number(attrs.updatedAt) || Date.now(),
      syncedAt: null,
      deletedAt: null,
    })
  }
  return notes
}

/** A foreign Markdown document becomes one note: first `# ` heading is the title, the rest is the body. */
function foreignNote(text: string, newId: () => string): Note {
  // Drop our own export banner if the document carries one but no note markers.
  const content = text.replace(/^\s*<!--\s*exported by tpt-pwa[^>]*-->\s*/, '').trim()
  const heading = content.match(/^#\s+(.+?)\s*$/m)
  const now = Date.now()
  return {
    id: newId(),
    title: heading ? heading[1].trim() : '',
    body: heading ? content.slice(content.indexOf(heading[0]) + heading[0].length).trim() : content,
    createdAt: now,
    updatedAt: now,
    syncedAt: null,
    deletedAt: null,
  }
}

/** Download filename for an export: `tpt-pwa-notes-2026-10-01T14-30.json`. */
export function exportFilename(ext: 'json' | 'md', now = new Date()): string {
  const stamp = now.toISOString().slice(0, 16).replace(':', '-')
  return `tpt-pwa-notes-${stamp}.${ext}`
}

// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { describe, expect, it } from 'vitest'
import { exportFilename, exportNotesJson, exportNotesMarkdown, parseNotesJson, parseNotesMarkdown } from './transfer'
import type { Note } from './storage'

function note(id: string, title: string, body: string): Note {
  return { id, title, body, createdAt: 1000, updatedAt: 2000, syncedAt: 1500, deletedAt: null }
}

describe('JSON export/import', () => {
  it('round-trips notes with ids and timestamps intact', () => {
    const notes = [note('n1', 'First', 'hello'), note('n2', '', 'untitled body')]
    const parsed = parseNotesJson(exportNotesJson(notes, new Date('2026-10-01T12:00:00Z')))
    expect(parsed).toEqual(notes)
    expect(exportNotesJson(notes, new Date('2026-10-01T12:00:00Z'))).toContain('"exportedAt": "2026-10-01T12:00:00.000Z"')
  })

  it('accepts a bare array of notes', () => {
    const parsed = parseNotesJson(JSON.stringify([note('n1', 'a', 'b')]))
    expect(parsed).toHaveLength(1)
    expect(parsed[0].id).toBe('n1')
  })

  it('rejects garbage with a friendly error', () => {
    expect(() => parseNotesJson('not json')).toThrow('not valid JSON')
    expect(() => parseNotesJson('{"unrelated": true}')).toThrow(/expected a tpt-pwa notes export/)
    expect(() => parseNotesJson('{"format": "tpt-pwa/notes"}')).toThrow(/expected a tpt-pwa notes export/)
  })
})

describe('Markdown export/import', () => {
  it('round-trips multiple notes with metadata markers', () => {
    const notes = [note('n1', 'Shopping', 'milk\n# not a title, just a loud line'), note('n2', '', ''), note('n3', 'Ideas', '## nested\n- one')]
    const md = exportNotesMarkdown(notes, new Date('2026-10-01T12:00:00Z'))

    const parsed = parseNotesMarkdown(md)
    expect(parsed.map((n) => n.id)).toEqual(['n1', 'n2', 'n3'])
    expect(parsed[0].title).toBe('Shopping')
    // A `# ` line inside the body must not split the note or become the title.
    expect(parsed[0].body).toContain('# not a title, just a loud line')
    // Empty titles round-trip as empty (the "Untitled" placeholder is not data).
    expect(parsed[1].title).toBe('')
    expect(parsed[2].body).toContain('## nested')
  })

  it('imports a foreign Markdown document as a single note', () => {
    const foreign = '# Meeting notes\n\nWe talked.\n\n- item one\n- item two\n'
    const [imported] = parseNotesMarkdown(foreign, () => 'fresh-id')
    expect(imported.id).toBe('fresh-id')
    expect(imported.title).toBe('Meeting notes')
    expect(imported.body).toBe('We talked.\n\n- item one\n- item two')
    // Imported notes are local edits: never pre-stamped as synced.
    expect(imported.syncedAt).toBeNull()
  })

  it('imports a marker-less document without a heading as one body-only note, banner stripped', () => {
    const [imported] = parseNotesMarkdown('<!-- exported by tpt-pwa 2026-10-01 -->\n\njust some text\n', () => 'id1')
    expect(imported.title).toBe('')
    expect(imported.body).toBe('just some text')
  })

  it('fills in missing marker metadata instead of NaN', () => {
    const md = '<!-- tpt-pwa-note id="n9" -->\n\n# Bare marker\n\nbody'
    const [imported] = parseNotesMarkdown(md)
    expect(imported.id).toBe('n9')
    expect(Number.isFinite(imported.createdAt)).toBe(true)
    expect(Number.isFinite(imported.updatedAt)).toBe(true)
  })
})

describe('exportFilename', () => {
  it('is filesystem-safe (no colons) and carries the extension', () => {
    const name = exportFilename('json', new Date('2026-10-01T14:30:00Z'))
    expect(name).toBe('tpt-pwa-notes-2026-10-01T14-30.json')
    expect(name).not.toContain(':')
    expect(exportFilename('md')).toMatch(/\.md$/)
  })
})

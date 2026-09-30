// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import { describe, expect, it } from 'vitest'
import { searchNotes } from './search'
import type { Note } from './storage'

function note(id: string, title: string, body: string, updatedAt = 100): Note {
  return { id, title, body, createdAt: updatedAt - 1, updatedAt, syncedAt: null, deletedAt: null }
}

const grocery = note('grocery', 'Grocery list', 'milk, eggs, bread', 300)
const recipe = note('recipe', 'Pancake recipe', 'milk, flour, eggs — whisk', 200)
const meeting = note('meeting', 'Team meeting', 'sync notes about the roadmap', 100)
const notes = [grocery, recipe, meeting]

describe('searchNotes', () => {
  it('empty or whitespace-only query returns the list unchanged (newest-first preserved)', () => {
    expect(searchNotes(notes, '')).toEqual(notes)
    expect(searchNotes(notes, '   ')).toEqual(notes)
  })

  it('matches are case-insensitive across title and body', () => {
    expect(searchNotes(notes, 'MILK').map((n) => n.id)).toEqual(['grocery', 'recipe'])
    expect(searchNotes(notes, 'roadmap').map((n) => n.id)).toEqual(['meeting'])
  })

  it('terms combine with AND semantics', () => {
    // Both terms must match, even across fields.
    expect(searchNotes(notes, 'pancake milk').map((n) => n.id)).toEqual(['recipe'])
    expect(searchNotes(notes, 'milk roadmap')).toEqual([])
  })

  it('title matches rank above body-only matches, recency breaks ties', () => {
    const a = note('a', 'milk', 'nothing relevant', 50)
    const b = note('b', 'milk', 'nothing relevant', 90)
    const c = note('c', 'buying milk tomorrow', 'nothing relevant', 10)
    // All three match in the title (equal scores), so recency decides.
    expect(searchNotes([a, b, c], 'milk').map((n) => n.id)).toEqual(['b', 'a', 'c'])

    // A body-only match loses to any title match regardless of recency.
    const bodyOnly = note('d', 'unrelated', 'some milk here', 999)
    expect(searchNotes([bodyOnly, c], 'milk').map((n) => n.id)).toEqual(['c', 'd'])
  })

  it('returns an empty list when nothing matches', () => {
    expect(searchNotes(notes, 'zebra')).toEqual([])
  })
})

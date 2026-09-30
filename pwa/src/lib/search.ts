// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import type { Note } from './storage'

/**
 * Client-side note search. The whole live list is already in memory (the
 * store keeps it newest-first), so search is a pure filter over it -- no
 * storage-level index to invalidate, works identically on every backend,
 * and degrades to nothing when the query is empty.
 *
 * Every whitespace-separated term must match somewhere (AND semantics);
 * notes whose TITLE matches all terms rank above body-only matches, with
 * recency breaking ties.
 */
export function searchNotes(notes: Note[], query: string): Note[] {
  const terms = query.trim().toLowerCase().split(/\s+/).filter((term) => term !== '')
  if (terms.length === 0) return notes

  const scored: Array<{ note: Note; score: number }> = []
  for (const note of notes) {
    const title = note.title.toLowerCase()
    const body = note.body.toLowerCase()
    let score = 0
    let matchedAll = true
    for (const term of terms) {
      const inTitle = title.includes(term)
      const inBody = body.includes(term)
      if (!inTitle && !inBody) {
        matchedAll = false
        break
      }
      // A single term may hit both; the strongest field counts once.
      score += inTitle ? 2 : 1
    }
    if (matchedAll) scored.push({ note, score })
  }

  return scored
    .sort((a, b) => b.score - a.score || b.note.updatedAt - a.note.updatedAt)
    .map(({ note }) => note)
}

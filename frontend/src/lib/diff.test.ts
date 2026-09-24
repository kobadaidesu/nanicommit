import { describe, expect, it } from 'vitest'
import { parseUnifiedDiff } from './diff'
import payload from '../data/commitPayload.json'

describe('parseUnifiedDiff on the real example payload', () => {
  const files = parseUnifiedDiff(payload.diff)

  it('finds every file in payload order, displayPath matching payload.files', () => {
    expect(files.map((f) => f.displayPath)).toEqual(payload.files)
  })

  it('classifies the rename with a space in the path (trailing-tab +++ line)', () => {
    const rename = files[0]
    expect(rename.kind).toBe('renamed')
    expect(rename.oldPath).toBe('docs/notes.md')
    expect(rename.newPath).toBe('docs/設計 メモ.md')
  })

  it('classifies the deletion and takes its old path', () => {
    const del = files[1]
    expect(del.kind).toBe('deleted')
    expect(del.displayPath).toBe('src/legacy.py')
    expect(del.newPath).toBeNull()
    expect(del.hunks[0].lines.every((l) => l.type === 'del')).toBe(true)
  })

  it('numbers the modified file hunk from @@ -1,6 +1,15 @@', () => {
    const mod = files[2]
    expect(mod.kind).toBe('modified')
    const h = mod.hunks[0]
    expect([h.oldStart, h.oldCount, h.newStart, h.newCount]).toEqual([1, 6, 1, 15])
    const first = h.lines[0]
    expect(first.type).toBe('context')
    expect(first.oldLine).toBe(1)
    expect(first.newLine).toBe(1)
    const firstDel = h.lines.find((l) => l.type === 'del')!
    expect(firstDel.oldLine).toBe(2)
    expect(firstDel.newLine).toBeNull()
    const firstAdd = h.lines.find((l) => l.type === 'add')!
    expect(firstAdd.oldLine).toBeNull()
    expect(firstAdd.newLine).toBe(2)
  })
})

describe('parseUnifiedDiff edge cases', () => {
  it('handles count-omitted hunk headers and section headings', () => {
    const diff =
      'diff --git a/a.txt b/a.txt\nindex 000..111 100644\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@ fn main\n-x\n+y\n'
    const [f] = parseUnifiedDiff(diff)
    expect(f.hunks[0].oldCount).toBe(1)
    expect(f.hunks[0].newCount).toBe(1)
    expect(f.hunks[0].sectionHeading).toBe('fn main')
  })

  it('handles added files and the no-newline marker', () => {
    const diff =
      'diff --git a/n.txt b/n.txt\nnew file mode 100644\nindex 000..111\n--- /dev/null\n+++ b/n.txt\n@@ -0,0 +1 @@\n+hello\n\\ No newline at end of file\n'
    const [f] = parseUnifiedDiff(diff)
    expect(f.kind).toBe('added')
    expect(f.oldPath).toBeNull()
    expect(f.hunks[0].lines[0].noNewlineMarker).toBe(true)
  })

  it('returns an empty list for an empty diff', () => {
    expect(parseUnifiedDiff('')).toEqual([])
  })
})

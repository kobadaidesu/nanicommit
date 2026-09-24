// Pure parser for the `diff` field of a commit payload: the concatenated
// unified-diff text that git diff-tree prints. No React, no dependencies.

export type FileChangeKind = 'modified' | 'added' | 'deleted' | 'renamed'

export interface DiffLine {
  type: 'context' | 'add' | 'del'
  oldLine: number | null
  newLine: number | null
  text: string
  /** True when git printed "\ No newline at end of file" after this line. */
  noNewlineMarker?: boolean
}

export interface Hunk {
  oldStart: number
  oldCount: number
  newStart: number
  newCount: number
  /** Trailing context after the second "@@", e.g. "class UserService:". */
  sectionHeading: string
  lines: DiffLine[]
}

export interface FileDiff {
  oldPath: string | null // null for added files
  newPath: string | null // null for deleted files
  /** The path the payload's `files` list uses: new path, old path for deletions. */
  displayPath: string
  kind: FileChangeKind
  hunks: Hunk[]
}

const HUNK_RE = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@ ?(.*)$/

// "--- a/path" / "+++ b/path" — git appends a tab when the path contains
// spaces, and prints /dev/null for the missing side.
function stripSide(line: string, marker: '--- ' | '+++ '): string | null {
  let p = line.slice(marker.length)
  if (p.endsWith('\t')) p = p.slice(0, -1)
  if (p === '/dev/null') return null
  return p.replace(/^[ab]\//, '')
}

export function parseUnifiedDiff(diff: string): FileDiff[] {
  const files: FileDiff[] = []
  let file: FileDiff | null = null
  let hunk: Hunk | null = null
  let oldNo = 0
  let newNo = 0
  let sawNewFileMode = false
  let sawDeletedFileMode = false
  let renamed = false

  const finishFile = () => {
    if (!file) return
    if (sawNewFileMode) file.kind = 'added'
    else if (sawDeletedFileMode) file.kind = 'deleted'
    else if (renamed) file.kind = 'renamed'
    if (file.oldPath === null) file.kind = 'added'
    if (file.newPath === null && file.oldPath !== null) file.kind = 'deleted'
    file.displayPath = file.newPath ?? file.oldPath ?? ''
    files.push(file)
  }

  const lines = diff.split('\n')
  if (lines.length > 0 && lines[lines.length - 1] === '') lines.pop()

  for (const line of lines) {
    if (line.startsWith('diff --git ')) {
      // Paths are NOT taken from this line: they are ambiguous when a path
      // contains spaces. The ---/+++/rename lines below are unambiguous.
      finishFile()
      file = { oldPath: null, newPath: null, displayPath: '', kind: 'modified', hunks: [] }
      hunk = null
      sawNewFileMode = false
      sawDeletedFileMode = false
      renamed = false
      continue
    }
    if (!file) continue

    if (hunk) {
      const c = line[0]
      if (c === ' ' || c === '+' || c === '-' || c === '\\' || line === '') {
        if (c === '\\') {
          const last = hunk.lines[hunk.lines.length - 1]
          if (last) last.noNewlineMarker = true
        } else if (c === '+') {
          hunk.lines.push({ type: 'add', oldLine: null, newLine: newNo++, text: line.slice(1) })
        } else if (c === '-') {
          hunk.lines.push({ type: 'del', oldLine: oldNo++, newLine: null, text: line.slice(1) })
        } else {
          hunk.lines.push({ type: 'context', oldLine: oldNo++, newLine: newNo++, text: line.slice(1) })
        }
        continue
      }
      hunk = null // a non-body line ends the hunk (next hunk header or file)
    }

    const m = HUNK_RE.exec(line)
    if (m) {
      hunk = {
        oldStart: Number(m[1]),
        oldCount: m[2] === undefined ? 1 : Number(m[2]),
        newStart: Number(m[3]),
        newCount: m[4] === undefined ? 1 : Number(m[4]),
        sectionHeading: (m[5] ?? '').trim(),
        lines: [],
      }
      oldNo = hunk.oldStart
      newNo = hunk.newStart
      file.hunks.push(hunk)
      continue
    }

    if (line.startsWith('--- ')) file.oldPath = stripSide(line, '--- ')
    else if (line.startsWith('+++ ')) file.newPath = stripSide(line, '+++ ')
    else if (line.startsWith('rename from ')) {
      renamed = true
      file.oldPath = line.slice('rename from '.length)
    } else if (line.startsWith('rename to ')) {
      renamed = true
      file.newPath = line.slice('rename to '.length)
    } else if (line.startsWith('new file mode ')) sawNewFileMode = true
    else if (line.startsWith('deleted file mode ')) sawDeletedFileMode = true
    // index / mode / similarity lines carry nothing the viewer needs.
  }
  finishFile()
  return files
}

export const changeKindLabel: Record<FileChangeKind, string> = {
  modified: '変更',
  added: '追加',
  deleted: '削除',
  renamed: '改名',
}

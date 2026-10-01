// 差分の左右分割表示の組み立て（lib/diff.ts の parseUnifiedDiff の結果を使う純粋な処理）。
import type { FileDiff, Hunk } from '../lib/diff'

export interface SideCell {
  no: number | null
  kind: 'ctx' | 'add' | 'del' | 'blank'
  text: string
}

export type SplitRow = { type: 'hunk'; left: string; right: string } | { type: 'line'; left: SideCell; right: SideCell }

const BLANK: SideCell = { no: null, kind: 'blank', text: '' }

/**
 * 1つのファイルの差分を、左（変更前）・右（変更後）の行の並びにする。
 * 連続する削除と追加は上から順に横に並べ、足りない側は空行で埋める（左右の行数が必ずそろう）。
 */
export function buildSplitRows(file: FileDiff): SplitRow[] {
  const rows: SplitRow[] = []
  for (const hunk of file.hunks) {
    rows.push(hunkRow(hunk))
    let dels: SideCell[] = []
    let adds: SideCell[] = []
    const flush = () => {
      for (let i = 0; i < Math.max(dels.length, adds.length); i++) {
        rows.push({ type: 'line', left: dels[i] ?? BLANK, right: adds[i] ?? BLANK })
      }
      dels = []
      adds = []
    }
    for (const line of hunk.lines) {
      if (line.type === 'del') dels.push({ no: line.oldLine, kind: 'del', text: line.text })
      else if (line.type === 'add') adds.push({ no: line.newLine, kind: 'add', text: line.text })
      else {
        flush()
        rows.push({
          type: 'line',
          left: { no: line.oldLine, kind: 'ctx', text: line.text },
          right: { no: line.newLine, kind: 'ctx', text: line.text },
        })
      }
    }
    flush()
  }
  return rows
}

function hunkRow(h: Hunk): SplitRow {
  const heading = h.sectionHeading ? ` ${h.sectionHeading}` : ''
  return {
    type: 'hunk',
    left: `@@ -${h.oldStart},${h.oldCount} @@${heading}`,
    right: `@@ +${h.newStart},${h.newCount} @@`,
  }
}

/** ファイルごとの追加・削除行数。 */
export function fileStats(file: FileDiff): { add: number; del: number } {
  let add = 0
  let del = 0
  for (const h of file.hunks) {
    for (const l of h.lines) {
      if (l.type === 'add') add++
      else if (l.type === 'del') del++
    }
  }
  return { add, del }
}

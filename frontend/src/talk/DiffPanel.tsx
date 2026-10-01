// 差分の左右分割表示（複数ファイルはタブで切り替え、たためる）。
import { useMemo, useState } from 'react'
import type { FileDiff } from '../lib/diff'
import { buildSplitRows } from './splitDiff'
import type { SideCell, SplitRow } from './splitDiff'

const MARK: Record<SideCell['kind'], string> = { add: '+', del: '-', ctx: ' ', blank: ' ' }

function Side({ rows, side }: { rows: SplitRow[]; side: 'left' | 'right' }) {
  return (
    <div className="talk-side">
      {rows.map((row, i) =>
        row.type === 'hunk' ? (
          <div key={i} className="talk-ln talk-hunk">
            {row[side]}
          </div>
        ) : (
          <div key={i} className={`talk-ln talk-${row[side].kind}`}>
            <span className="talk-no">{row[side].no ?? ''}</span>
            <span>{MARK[row[side].kind]}</span>
            <span>{row[side].text}</span>
          </div>
        ),
      )}
    </div>
  )
}

export function DiffPanel({ files }: { files: FileDiff[] }) {
  const [index, setIndex] = useState(0)
  const [open, setOpen] = useState(true)
  const file = files[Math.min(index, files.length - 1)]
  const rows = useMemo(() => (file ? buildSplitRows(file) : []), [file])

  return (
    <div className="talk-diffcard">
      <div className="talk-diffhead">
        <span aria-hidden="true">📄</span>
        <div className="talk-filetabs" role="tablist">
          {files.length === 0 ? (
            <span>差分なし</span>
          ) : (
            files.map((f, i) => (
              <button
                key={f.displayPath + i}
                className="talk-filetab"
                type="button"
                role="tab"
                aria-selected={i === index}
                onClick={() => {
                  setIndex(i)
                  setOpen(true)
                }}
              >
                {f.displayPath}
              </button>
            ))
          )}
        </div>
        <button className="talk-fold" type="button" onClick={() => setOpen(!open)} aria-expanded={open}>
          {open ? 'たたむ ▴' : 'ひらく ▾'}
        </button>
      </div>
      {open &&
        (rows.length > 0 ? (
          <div className="talk-split">
            <Side rows={rows} side="left" />
            <Side rows={rows} side="right" />
          </div>
        ) : (
          <div className="talk-nodiff">表示できる差分がありません（バイナリや、送信時に除外されたファイル）。</div>
        ))}
    </div>
  )
}

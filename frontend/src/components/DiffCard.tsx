import { useMemo, useState } from 'react'
import type { CommitPayload } from '../types/payload'
import { changeKindLabel, parseUnifiedDiff } from '../lib/diff'
import { DiffViewer } from './DiffViewer'
import { ChevronDownIcon, ChevronLeftIcon, ChevronRightIcon, FileIcon } from './Icons'

interface Props {
  commit: CommitPayload
}

export function DiffCard({ commit }: Props) {
  const fileDiffs = useMemo(() => parseUnifiedDiff(commit.diff), [commit.diff])
  const [index, setIndex] = useState(0)
  const current = fileDiffs[index]

  return (
    <section className="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm">
      <div className="mb-4 flex items-center gap-3">
        <FileIcon className="h-5 w-5 text-gray-700" />
        <h2 className="text-base font-bold text-gray-900">変更されたコードの差分</h2>
        <span className="rounded-md bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-600">
          {commit.files.length} files
        </span>
        <div className="ml-auto flex items-center gap-2">
          <div className="relative">
            <select
              value={index}
              onChange={(e) => setIndex(Number(e.target.value))}
              className="appearance-none rounded-lg border border-gray-300 bg-white py-1.5 pl-3 pr-8 font-mono text-xs text-gray-800"
            >
              {fileDiffs.map((f, i) => (
                <option key={f.displayPath} value={i}>
                  {f.displayPath}
                  {f.kind !== 'modified' ? `(${changeKindLabel[f.kind]})` : ''}
                </option>
              ))}
            </select>
            <ChevronDownIcon className="pointer-events-none absolute right-2 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-500" />
          </div>
          <button
            onClick={() => setIndex((i) => Math.max(0, i - 1))}
            disabled={index === 0}
            className="rounded-lg border border-gray-300 p-1.5 text-gray-600 disabled:opacity-40"
          >
            <ChevronLeftIcon className="h-4 w-4" />
          </button>
          <button
            onClick={() => setIndex((i) => Math.min(fileDiffs.length - 1, i + 1))}
            disabled={index === fileDiffs.length - 1}
            className="rounded-lg border border-gray-300 p-1.5 text-gray-600 disabled:opacity-40"
          >
            <ChevronRightIcon className="h-4 w-4" />
          </button>
        </div>
      </div>
      {current ? (
        <DiffViewer file={current} />
      ) : (
        <div className="p-6 text-center text-sm text-gray-500">差分がありません</div>
      )}
    </section>
  )
}

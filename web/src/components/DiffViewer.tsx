import type { FileDiff } from '../lib/diff'
import { changeKindLabel } from '../lib/diff'

interface Props {
  file: FileDiff
}

export function DiffViewer({ file }: Props) {
  if (file.hunks.length === 0) {
    return (
      <div className="rounded-lg border border-gray-200 p-6 text-center text-sm text-gray-500">
        変更なし({changeKindLabel[file.kind]}のみ)
        {file.kind === 'renamed' && (
          <div className="mt-1 font-mono text-xs">
            {file.oldPath} → {file.newPath}
          </div>
        )}
      </div>
    )
  }

  return (
    <div className="overflow-x-auto rounded-lg border border-gray-200">
      <table className="w-full border-collapse font-mono text-[13px] leading-6">
        <tbody>
          {file.hunks.map((h, hi) => (
            <HunkRows key={hi} hunk={h} />
          ))}
        </tbody>
      </table>
    </div>
  )
}

function HunkRows({ hunk }: { hunk: FileDiff['hunks'][number] }) {
  return (
    <>
      <tr className="bg-gray-100 text-gray-500">
        <td colSpan={3} className="whitespace-pre px-4 py-1">
          {`@@ -${hunk.oldStart},${hunk.oldCount} +${hunk.newStart},${hunk.newCount} @@`}
          {hunk.sectionHeading ? ` ${hunk.sectionHeading}` : ''}
        </td>
      </tr>
      {hunk.lines.map((line, i) => {
        const rowBg =
          line.type === 'add' ? 'bg-green-50' : line.type === 'del' ? 'bg-red-50' : 'bg-white'
        const numBg =
          line.type === 'add' ? 'bg-green-100/60' : line.type === 'del' ? 'bg-red-100/60' : ''
        const sign = line.type === 'add' ? '+' : line.type === 'del' ? '-' : ' '
        return (
          <tr key={i} className={rowBg}>
            <td className={`w-12 select-none px-2 text-right text-gray-400 ${numBg}`}>
              {line.oldLine ?? ''}
            </td>
            <td className={`w-12 select-none px-2 text-right text-gray-400 ${numBg}`}>
              {line.newLine ?? ''}
            </td>
            <td className="whitespace-pre px-3 text-gray-800">
              <span
                className={
                  'mr-3 inline-block w-3 select-none ' +
                  (line.type === 'add' ? 'text-green-600' : line.type === 'del' ? 'text-red-500' : 'text-transparent')
                }
              >
                {sign}
              </span>
              {line.text}
              {line.noNewlineMarker && (
                <span className="ml-2 select-none text-xs text-gray-400">(改行なし)</span>
              )}
            </td>
          </tr>
        )
      })}
    </>
  )
}

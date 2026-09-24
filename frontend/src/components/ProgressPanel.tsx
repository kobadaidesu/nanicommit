import { TargetIcon } from './Icons'

interface Props {
  solved: number
  total: number
  score: number
  totalPoints: number
  unlocked: boolean
}

export function ProgressPanel({ solved, total, score, totalPoints, unlocked }: Props) {
  const remaining = total - solved
  return (
    <section className="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm">
      <h2 className="mb-4 border-l-4 border-blue-600 pl-2 text-base font-bold text-gray-900">学習の進捗</h2>
      <div className="mb-4 flex items-center justify-around">
        <ProgressRing value={solved} max={total} />
        <div>
          <div className="text-sm text-gray-500">スコア</div>
          <div className="text-2xl font-bold text-gray-900">
            {score} <span className="text-base font-medium text-gray-500">/ {totalPoints} 点</span>
          </div>
        </div>
      </div>
      <div
        className={
          'flex items-center gap-3 rounded-xl p-4 text-sm font-semibold ' +
          (unlocked ? 'bg-green-50 text-green-700' : 'bg-blue-50 text-blue-700')
        }
      >
        <TargetIcon className="h-6 w-6 shrink-0" />
        {unlocked ? (
          <span>すべて正解しました! push を許可できます</span>
        ) : (
          <span>
            あと {remaining} 問正解して
            <br />
            push を許可しましょう!
          </span>
        )}
      </div>
    </section>
  )
}

function ProgressRing({ value, max }: { value: number; max: number }) {
  const r = 42
  const c = 2 * Math.PI * r
  const ratio = max === 0 ? 0 : value / max
  return (
    <div className="relative h-28 w-28">
      <svg viewBox="0 0 100 100" className="h-full w-full -rotate-90">
        <circle cx="50" cy="50" r={r} fill="none" stroke="#e5e7eb" strokeWidth="9" />
        <circle
          cx="50"
          cy="50"
          r={r}
          fill="none"
          stroke="#16a34a"
          strokeWidth="9"
          strokeLinecap="round"
          strokeDasharray={c}
          strokeDashoffset={c * (1 - ratio)}
          className="transition-all duration-500"
        />
      </svg>
      <div className="absolute inset-0 flex flex-col items-center justify-center">
        <div className="text-xl font-bold text-gray-900">
          {value} <span className="text-sm font-medium text-gray-500">/ {max}</span>
        </div>
        <div className="text-xs text-gray-500">完了</div>
      </div>
    </div>
  )
}

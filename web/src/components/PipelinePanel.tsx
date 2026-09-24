import { uiMeta } from '../data/uiMeta'
import { CheckIcon } from './Icons'

interface Props {
  completed: boolean
  pushGranted: boolean
}

type StepState = 'done' | 'active' | 'pending'

interface Step {
  label: string
  detail: string
  state: StepState
  chip?: string
}

export function PipelinePanel({ completed, pushGranted }: Props) {
  const steps: Step[] = [
    { label: '差分の取得', detail: 'コミット内容を解析しました', state: 'done', chip: '完了' },
    { label: '問題の生成', detail: '変更内容に基づいて問題を作成', state: 'done', chip: '完了' },
    completed
      ? { label: '問題に回答', detail: 'すべての問題に回答しました', state: 'done', chip: '完了' }
      : { label: '現在:問題に回答中', detail: 'コードを読んで回答してください', state: 'active', chip: '進行中' },
    { label: '採点', detail: '回答を評価します', state: completed ? 'done' : 'pending', chip: completed ? '完了' : undefined },
    { label: '合格', detail: 'すべての問題に正解すると…', state: completed ? 'done' : 'pending', chip: completed ? '完了' : undefined },
    pushGranted
      ? { label: 'push 許可', detail: 'git push が実行可能になりました', state: 'done', chip: '完了' }
      : { label: 'push 許可', detail: 'git push が実行可能になります', state: completed ? 'active' : 'pending', chip: completed ? '進行中' : undefined },
  ]

  return (
    <>
      <section className="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm">
        <h2 className="mb-4 border-l-4 border-blue-600 pl-2 text-base font-bold text-gray-900">処理の流れ</h2>
        <ol className="flex flex-col">
          {steps.map((s, i) => (
            <li key={s.label} className="relative flex gap-3 pb-5 last:pb-0">
              {i < steps.length - 1 && (
                <span className="absolute left-[9px] top-6 h-[calc(100%-1.25rem)] w-px bg-gray-200" />
              )}
              <StepDot state={s.state} />
              <div className="min-w-0 flex-1">
                <div className="flex items-center justify-between gap-2">
                  <span
                    className={
                      'text-sm font-semibold ' +
                      (s.state === 'active' ? 'text-blue-700' : s.state === 'done' ? 'text-gray-800' : 'text-gray-400')
                    }
                  >
                    {s.label}
                  </span>
                  {s.chip && (
                    <span
                      className={
                        'rounded-md px-2 py-0.5 text-xs font-medium ' +
                        (s.chip === '完了' ? 'bg-green-50 text-green-600' : 'bg-blue-50 text-blue-600')
                      }
                    >
                      {s.chip}
                    </span>
                  )}
                </div>
                <div className={'text-xs ' + (s.state === 'pending' ? 'text-gray-400' : 'text-gray-500')}>
                  {s.detail}
                </div>
              </div>
            </li>
          ))}
        </ol>
      </section>
      <section className="rounded-2xl border border-gray-200 bg-white p-5 text-center text-sm leading-relaxed text-gray-600 shadow-sm">
        {uiMeta.quote}
      </section>
    </>
  )
}

function StepDot({ state }: { state: StepState }) {
  if (state === 'done') {
    return (
      <span className="z-[1] flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-green-500">
        <CheckIcon className="h-3 w-3 text-white" />
      </span>
    )
  }
  if (state === 'active') {
    return (
      <span className="z-[1] flex h-5 w-5 shrink-0 items-center justify-center rounded-full border-2 border-blue-600 bg-white">
        <span className="h-2 w-2 animate-pulse rounded-full bg-blue-600" />
      </span>
    )
  }
  return <span className="z-[1] h-5 w-5 shrink-0 rounded-full border-2 border-gray-300 bg-white" />
}

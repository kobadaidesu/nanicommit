import type { QuizQuestion } from '../types/quiz'
import type { QuizState } from '../state/quizReducer'
import { CheckIcon, ChevronRightIcon } from './Icons'

interface Props {
  question: QuizQuestion
  index: number
  total: number
  quiz: QuizState
  onSelect: (choiceId: string) => void
  onSubmit: () => void
  onNext: () => void
  onRetry: () => void
}

export function QuizCard({ question, index, total, quiz, onSelect, onSubmit, onNext, onRetry }: Props) {
  const { phase, selectedChoiceId, lastResult } = quiz
  const feedback = phase === 'feedback' ? lastResult : null

  if (phase === 'completed') {
    return (
      <section className="rounded-2xl border border-gray-200 bg-white p-8 text-center shadow-sm">
        <div className="mx-auto mb-3 flex h-12 w-12 items-center justify-center rounded-full bg-green-100">
          <CheckIcon className="h-6 w-6 text-green-600" />
        </div>
        <h2 className="text-lg font-bold text-gray-900">すべての問題に正解しました!</h2>
        <p className="mt-1 text-sm text-gray-600">
          左の「push を許可」ボタンから git push を解禁できます。
        </p>
      </section>
    )
  }

  return (
    <section className="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm">
      <div className="mb-4 flex items-center gap-3">
        <span className="flex h-6 w-6 items-center justify-center rounded-md bg-blue-50 text-xs font-bold text-blue-700">
          Q
        </span>
        <h2 className="text-base font-bold text-gray-900">
          問題 {index + 1} / {total}
        </h2>
        <span className="rounded-full bg-gray-100 px-3 py-0.5 text-xs text-gray-600">{question.category}</span>
        <span className="ml-auto rounded-md bg-blue-50 px-2.5 py-1 text-xs font-semibold text-blue-700">
          {question.points} 点
        </span>
      </div>

      <p className="mb-5 text-lg font-bold leading-relaxed text-gray-900">
        <span className="mr-2">Q.</span>
        {question.question}
      </p>

      <div className="mb-5 flex flex-col gap-3">
        {question.choices.map((c) => {
          const selected = selectedChoiceId === c.id
          let styles = 'border-gray-200 hover:border-gray-300'
          if (feedback) {
            if (c.id === feedback.correct_choice_id && feedback.correct) {
              styles = 'border-green-500 bg-green-50'
            } else if (selected && !feedback.correct) {
              styles = 'border-red-400 bg-red-50'
            } else {
              styles = 'border-gray-200 opacity-60'
            }
          } else if (selected) {
            styles = 'border-blue-500 bg-blue-50'
          }
          return (
            <button
              key={c.id}
              role="radio"
              aria-checked={selected}
              disabled={phase === 'feedback'}
              onClick={() => onSelect(c.id)}
              className={`flex items-center gap-3 rounded-xl border-2 px-4 py-3 text-left text-sm text-gray-800 transition-colors ${styles}`}
            >
              <span
                className={
                  'flex h-4 w-4 shrink-0 items-center justify-center rounded-full border-2 ' +
                  (selected ? 'border-blue-600' : 'border-gray-300')
                }
              >
                {selected && <span className="h-2 w-2 rounded-full bg-blue-600" />}
              </span>
              <span className="w-4 shrink-0 font-semibold text-gray-500">{c.id}</span>
              {c.label}
            </button>
          )
        })}
      </div>

      {feedback && (
        <div
          className={
            'mb-4 rounded-lg px-4 py-3 text-sm font-medium ' +
            (feedback.correct ? 'bg-green-50 text-green-700' : 'bg-red-50 text-red-600')
          }
        >
          {feedback.correct
            ? '✓ 正解です!'
            : '✗ 不正解です。もう一度、差分を読み直して考えてみましょう。'}
        </div>
      )}

      <div className="flex items-end justify-between gap-4">
        {question.hint ? (
          <div className="text-sm">
            <div className="mb-1 font-semibold text-amber-500">💡 ヒント</div>
            <p className="max-w-xl leading-relaxed text-gray-500">{question.hint}</p>
          </div>
        ) : (
          <span />
        )}
        {feedback ? (
          feedback.correct ? (
            <button
              onClick={onNext}
              className="flex shrink-0 items-center gap-2 rounded-xl bg-blue-600 px-6 py-2.5 text-sm font-semibold text-white hover:bg-blue-700"
            >
              {index + 1 === total ? '完了' : '次の問題へ'}
              <ChevronRightIcon className="h-4 w-4" />
            </button>
          ) : (
            <button
              onClick={onRetry}
              className="shrink-0 rounded-xl bg-gray-700 px-6 py-2.5 text-sm font-semibold text-white hover:bg-gray-800"
            >
              もう一度
            </button>
          )
        ) : (
          <button
            onClick={onSubmit}
            disabled={!selectedChoiceId}
            className="flex shrink-0 items-center gap-2 rounded-xl bg-blue-600 px-6 py-2.5 text-sm font-semibold text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:bg-gray-300"
          >
            回答する
            <ChevronRightIcon className="h-4 w-4" />
          </button>
        )}
      </div>
    </section>
  )
}

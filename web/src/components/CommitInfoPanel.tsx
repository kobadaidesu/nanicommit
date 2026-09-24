import { useState } from 'react'
import type { CommitPayload } from '../types/payload'
import type { QuizQuestion } from '../types/quiz'
import type { QuizState } from '../state/quizReducer'
import { correctCount } from '../state/quizReducer'
import { BranchIcon, CheckIcon, ChevronRightIcon, CopyIcon, FileIcon, LockIcon } from './Icons'

interface Props {
  commit: CommitPayload
  questions: QuizQuestion[]
  quiz: QuizState
  unlocked: boolean
  pushGranted: boolean
  onGrantPush: () => void
}

export function CommitInfoPanel({ commit, questions, quiz, unlocked, pushGranted, onGrantPush }: Props) {
  const [copied, setCopied] = useState(false)
  const shortSha = commit.commit_sha.slice(0, 7)
  const solved = correctCount(quiz)

  const copySha = () => {
    void navigator.clipboard?.writeText(commit.commit_sha)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  return (
    <aside className="flex flex-col gap-5 rounded-2xl border border-gray-200 bg-white p-5 shadow-sm">
      <h2 className="flex items-center gap-2 text-base font-bold text-gray-900">
        <span className="h-4 w-4 rounded-full border-2 border-gray-800" />
        コミット情報
      </h2>

      <section>
        <div className="mb-1.5 text-sm text-gray-500">コミットメッセージ</div>
        <div className="whitespace-pre-line rounded-lg bg-gray-50 p-3 text-sm leading-relaxed text-gray-800">
          {commit.message.trim()}
        </div>
      </section>

      <section>
        <div className="mb-1.5 flex items-center gap-2 text-sm text-gray-500">
          コミットID(短縮)
          <button onClick={copySha} className="text-gray-400 hover:text-gray-600" title="コピー">
            {copied ? <CheckIcon className="h-3.5 w-3.5 text-green-600" /> : <CopyIcon className="h-3.5 w-3.5" />}
          </button>
        </div>
        <button
          onClick={copySha}
          className="flex items-center gap-1.5 rounded-md bg-blue-50 px-2.5 py-1 font-mono text-sm text-blue-700"
        >
          <CopyIcon className="h-3.5 w-3.5" />
          {shortSha}
        </button>
      </section>

      <section>
        <div className="mb-1.5 text-sm text-gray-500">ブランチ</div>
        <div className="flex items-center gap-1.5 text-sm font-medium text-blue-700">
          <BranchIcon className="h-4 w-4" />
          {commit.branch ?? 'detached HEAD'}
        </div>
      </section>

      <section>
        <div className="mb-1.5 text-sm text-gray-500">変更ファイル数</div>
        <div className="flex items-center gap-1.5 text-sm text-gray-800">
          <FileIcon className="h-4 w-4 text-gray-400" />
          {commit.files.length} files
          <ChevronRightIcon className="ml-auto h-4 w-4 text-gray-400" />
        </div>
      </section>

      <section>
        <div className="mb-2 flex items-center justify-between text-sm">
          <span className="font-semibold text-gray-800">問題の進捗</span>
          <span className="text-gray-500">
            {solved} / {questions.length}
          </span>
        </div>
        <div className="mb-4 h-1.5 overflow-hidden rounded-full bg-gray-200">
          <div
            className="h-full rounded-full bg-blue-600 transition-all"
            style={{ width: `${(solved / questions.length) * 100}%` }}
          />
        </div>
        <ol className="flex flex-col gap-4">
          {questions.map((q, i) => {
            const done = quiz.answers[q.id]?.correct ?? false
            const active = !done && i === quiz.currentIndex && quiz.phase !== 'completed'
            return (
              <li key={q.id} className="flex items-start gap-3">
                <span
                  className={
                    'mt-0.5 flex h-4 w-4 items-center justify-center rounded-full border-2 ' +
                    (done
                      ? 'border-blue-600 bg-blue-600'
                      : active
                        ? 'border-blue-600 bg-white'
                        : 'border-gray-300 bg-white')
                  }
                >
                  {done && <CheckIcon className="h-2.5 w-2.5 text-white" />}
                  {active && <span className="h-1.5 w-1.5 rounded-full bg-blue-600" />}
                </span>
                <div>
                  <div className={'text-sm font-semibold ' + (active || done ? 'text-blue-700' : 'text-gray-500')}>
                    問題 {i + 1}
                  </div>
                  <div className="text-xs text-gray-500">{q.title}</div>
                </div>
              </li>
            )
          })}
        </ol>
      </section>

      <section className="rounded-xl bg-gray-50 p-4">
        <div className="mb-3 flex items-start gap-2 text-sm text-gray-600">
          <LockIcon className="mt-0.5 h-4 w-4 shrink-0 text-gray-500" />
          {unlocked ? 'すべての問題に正解しました。push を許可できます' : 'すべての問題に正解すると push が許可されます'}
        </div>
        <button
          disabled={!unlocked || pushGranted}
          onClick={onGrantPush}
          className={
            'flex w-full items-center justify-center gap-2 rounded-lg py-2.5 text-sm font-semibold transition-colors ' +
            (pushGranted
              ? 'bg-green-600 text-white'
              : unlocked
                ? 'bg-blue-600 text-white hover:bg-blue-700'
                : 'cursor-not-allowed bg-gray-300 text-white')
          }
        >
          {pushGranted ? (
            <>
              <CheckIcon className="h-4 w-4" /> push 許可済み
            </>
          ) : (
            <>
              <BranchIcon className="h-4 w-4" /> push を許可
            </>
          )}
        </button>
      </section>
    </aside>
  )
}

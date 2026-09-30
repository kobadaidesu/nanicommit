import { useEffect, useReducer, useState } from 'react'
import { ApiError, getSession, gradeAnswer } from './data/api'
import type { ApiErrorKind } from './data/api'
import type { SessionResponse } from './types/quiz'
import {
  correctCount,
  initialQuizState,
  pushUnlocked,
  quizReducer,
  score,
  totalPoints,
} from './state/quizReducer'
import { TopBar } from './components/TopBar'
import { CommitInfoPanel } from './components/CommitInfoPanel'
import { DiffCard } from './components/DiffCard'
import { QuizCard } from './components/QuizCard'
import { ProgressPanel } from './components/ProgressPanel'
import { PipelinePanel } from './components/PipelinePanel'

const ERROR_MESSAGES: Record<ApiErrorKind, { title: string; body: string }> = {
  no_quiz_id: {
    title: 'クイズのURLを開いてください',
    body: 'このページは /quizzes/<ID> の形で開きます。commit したときに CLI が表示する quiz URL を使ってください。',
  },
  unauthenticated: {
    title: 'ログインが必要です',
    body: '問題の閲覧と回答には GitHub ログインが必要です。ログイン後、もう一度このページを開いてください。',
  },
  not_found: {
    title: 'クイズが見つかりません',
    body: 'URL が正しいか、自分の commit のクイズかを確認してください（他人のクイズは表示できません）。',
  },
  server: {
    title: 'バックエンドに接続できません',
    body: 'サーバーが起動しているか確認して、再読み込みしてください。',
  },
}

function ErrorScreen({ error }: { error: ApiError }) {
  const m = ERROR_MESSAGES[error.kind]
  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50 px-4">
      <div className="w-full max-w-md rounded-xl border border-gray-200 bg-white p-8 text-center shadow-sm">
        <h1 className="text-lg font-bold text-gray-900">{m.title}</h1>
        <p className="mt-2 text-sm leading-relaxed text-gray-600">{m.body}</p>
        {error.kind === 'server' && error.message !== 'server' && (
          <p className="mt-2 text-xs text-gray-400">{error.message}</p>
        )}
        {error.kind === 'unauthenticated' && (
          <a
            href="/connect"
            className="mt-6 inline-block rounded-lg bg-gray-900 px-5 py-2.5 text-sm font-semibold text-white hover:bg-gray-700"
          >
            ログインページへ
          </a>
        )}
      </div>
    </div>
  )
}

export default function App() {
  const [session, setSession] = useState<SessionResponse | null>(null)
  const [error, setError] = useState<ApiError | null>(null)
  const [quiz, dispatch] = useReducer(quizReducer, initialQuizState)
  const [pushGranted, setPushGranted] = useState(false)

  useEffect(() => {
    getSession()
      .then(setSession)
      .catch((e: unknown) => setError(e instanceof ApiError ? e : new ApiError('server', String(e))))
  }, [])

  if (error) {
    return <ErrorScreen error={error} />
  }
  if (!session) {
    return <div className="flex min-h-screen items-center justify-center text-gray-500">読み込み中…</div>
  }

  const { commit, quiz: quizSession } = session
  const questions = quizSession.questions
  const question = questions[Math.min(quiz.currentIndex, questions.length - 1)]
  const unlocked = pushUnlocked(quiz, questions)

  const submit = () => {
    if (!quiz.selectedChoiceId) return
    const choiceId = quiz.selectedChoiceId
    gradeAnswer(question.id, choiceId)
      .then((result) => dispatch({ type: 'ANSWER_GRADED', result, choiceId }))
      .catch((e: unknown) => setError(e instanceof ApiError ? e : new ApiError('server', String(e))))
  }

  return (
    <div className="min-h-screen bg-gray-100 font-sans text-gray-900">
      <TopBar branch={commit.branch} unlocked={unlocked} />
      <main className="mx-auto grid max-w-[1440px] items-start gap-5 p-6 lg:grid-cols-[300px_minmax(0,1fr)_320px]">
        <CommitInfoPanel
          commit={commit}
          questions={questions}
          quiz={quiz}
          unlocked={unlocked}
          pushGranted={pushGranted}
          onGrantPush={() => setPushGranted(true)}
        />
        <div className="flex flex-col gap-5">
          <DiffCard commit={commit} />
          <QuizCard
            question={question}
            index={quiz.currentIndex}
            total={questions.length}
            quiz={quiz}
            onSelect={(choiceId) => dispatch({ type: 'SELECT_CHOICE', choiceId })}
            onSubmit={submit}
            onNext={() => dispatch({ type: 'NEXT_QUESTION', total: questions.length })}
            onRetry={() => dispatch({ type: 'RETRY' })}
          />
        </div>
        <div className="flex flex-col gap-5">
          <ProgressPanel
            solved={correctCount(quiz)}
            total={questions.length}
            score={score(quiz, questions)}
            totalPoints={totalPoints(questions)}
            unlocked={unlocked}
          />
          <PipelinePanel completed={quiz.phase === 'completed'} pushGranted={pushGranted} />
        </div>
      </main>
    </div>
  )
}

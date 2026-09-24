import { useEffect, useReducer, useState } from 'react'
import { getSession, gradeAnswer } from './data/api'
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

export default function App() {
  const [session, setSession] = useState<SessionResponse | null>(null)
  const [quiz, dispatch] = useReducer(quizReducer, initialQuizState)
  const [pushGranted, setPushGranted] = useState(false)

  useEffect(() => {
    void getSession().then(setSession)
  }, [])

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
    void gradeAnswer(question.id, choiceId).then((result) =>
      dispatch({ type: 'ANSWER_GRADED', result, choiceId }),
    )
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

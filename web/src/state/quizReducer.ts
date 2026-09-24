import type { GradeResult, QuizQuestion } from '../types/quiz'

export type Phase = 'answering' | 'feedback' | 'completed'

export interface QuizState {
  currentIndex: number
  selectedChoiceId: string | null
  phase: Phase
  lastResult: GradeResult | null
  /** question id -> the answer that was graded (latest attempt). */
  answers: Record<string, { choiceId: string; correct: boolean }>
}

export const initialQuizState: QuizState = {
  currentIndex: 0,
  selectedChoiceId: null,
  phase: 'answering',
  lastResult: null,
  answers: {},
}

export type QuizAction =
  | { type: 'SELECT_CHOICE'; choiceId: string }
  | { type: 'ANSWER_GRADED'; result: GradeResult; choiceId: string }
  | { type: 'NEXT_QUESTION'; total: number }
  | { type: 'RETRY' }

export function quizReducer(state: QuizState, action: QuizAction): QuizState {
  switch (action.type) {
    case 'SELECT_CHOICE':
      if (state.phase !== 'answering') return state
      return { ...state, selectedChoiceId: action.choiceId }
    case 'ANSWER_GRADED':
      return {
        ...state,
        phase: 'feedback',
        lastResult: action.result,
        answers: {
          ...state.answers,
          [action.result.question_id]: {
            choiceId: action.choiceId,
            correct: action.result.correct,
          },
        },
      }
    case 'NEXT_QUESTION': {
      const next = state.currentIndex + 1
      if (next >= action.total) return { ...state, phase: 'completed', lastResult: null }
      return {
        ...state,
        currentIndex: next,
        selectedChoiceId: null,
        phase: 'answering',
        lastResult: null,
      }
    }
    case 'RETRY':
      return { ...state, phase: 'answering', selectedChoiceId: null, lastResult: null }
  }
}

export function correctCount(state: QuizState): number {
  return Object.values(state.answers).filter((a) => a.correct).length
}

export function score(state: QuizState, questions: QuizQuestion[]): number {
  return questions.reduce(
    (sum, q) => sum + (state.answers[q.id]?.correct ? q.points : 0),
    0,
  )
}

export function totalPoints(questions: QuizQuestion[]): number {
  return questions.reduce((sum, q) => sum + q.points, 0)
}

export function pushUnlocked(state: QuizState, questions: QuizQuestion[]): boolean {
  return state.phase === 'completed' && correctCount(state) === questions.length
}

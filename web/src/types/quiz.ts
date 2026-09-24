// PROPOSED backend API contract for the quiz part of CommitCoach.
// Nothing here is implemented server-side yet — this file doubles as the
// draft the backend can implement against. Note that the correct choice is
// deliberately NOT part of QuizQuestion: a real API must not ship answers to
// the client, so grading goes through gradeAnswer() (see data/api.ts).
import type { CommitPayload } from './payload'

export interface QuizChoice {
  id: string // "A" | "B" | "C" | "D"
  label: string
}

export interface QuizQuestion {
  id: string
  /** Short label used in the left stepper, e.g. "キャッシュ無効化の理由". */
  title: string
  /** Category chip next to the question number, e.g. "実装の意図を理解する". */
  category: string
  points: number
  /** Full question text (without the leading "Q."). */
  question: string
  choices: QuizChoice[]
  hint?: string
}

export interface QuizSession {
  session_id: string
  repository_id: string
  commit_sha: string
  pass_policy: { require_all_correct: boolean }
  questions: QuizQuestion[]
}

export interface GradeResult {
  question_id: string
  correct: boolean
  correct_choice_id: string
}

export interface SessionResponse {
  commit: CommitPayload
  quiz: QuizSession
}

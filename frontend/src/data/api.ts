// Data-access layer for the quiz screen, backed by the real API (D3/D4).
// The UI keeps its own view types (types/quiz.ts); this module adapts the
// backend responses to them so components stay unchanged.
import type { CommitPayload } from '../types/payload'
import type { GradeResult, QuizQuestion, SessionResponse } from '../types/quiz'
import { supabase } from '../lib/supabase'

const API_BASE = (import.meta.env.VITE_API_BASE_URL as string | undefined) ?? 'http://localhost:8100'

export type ApiErrorKind = 'no_quiz_id' | 'unauthenticated' | 'not_found' | 'server'

export class ApiError extends Error {
  constructor(
    public kind: ApiErrorKind,
    message?: string,
  ) {
    super(message ?? kind)
  }
}

const CHOICE_IDS = ['A', 'B', 'C', 'D']

// D3 response (backend/app/schemas.py QuizDetailOut).
interface QuizDetailOut {
  quiz_id: string
  status: 'ready' | 'passed'
  commit: CommitPayload & { repository_name: string }
  questions: {
    question_id: string
    position: number
    question: string
    choices: string[]
    hint: string
    solved: boolean
  }[]
  progress: { solved_count: number; total_count: number }
}

// D4 response (backend/app/schemas.py AnswerOut).
interface AnswerOut {
  question_id: string
  correct: boolean
  feedback: string
  progress: { solved_count: number; total_count: number }
  passed: boolean
}

/** The quiz id comes from the page URL: /quizzes/<uuid> (the CLI prints it). */
function quizIdFromPath(): string {
  const m = window.location.pathname.match(/\/quizzes\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/i)
  if (!m) throw new ApiError('no_quiz_id')
  return m[1].toLowerCase()
}

async function authHeaders(): Promise<Record<string, string>> {
  if (!supabase) throw new ApiError('unauthenticated', 'Supabase が設定されていません')
  const { data } = await supabase.auth.getSession()
  const token = data.session?.access_token
  if (!token) throw new ApiError('unauthenticated')
  return { Authorization: `Bearer ${token}` }
}

async function request<T>(path: string, init: RequestInit): Promise<T> {
  let res: Response
  try {
    res = await fetch(`${API_BASE}${path}`, init)
  } catch {
    throw new ApiError('server', `バックエンド (${API_BASE}) に接続できません`)
  }
  if (res.status === 401) throw new ApiError('unauthenticated')
  if (res.status === 404) throw new ApiError('not_found')
  if (!res.ok) {
    const detail = await res
      .json()
      .then((b: { detail?: unknown }) => (typeof b.detail === 'string' ? b.detail : undefined))
      .catch(() => undefined)
    throw new ApiError('server', detail ?? `HTTP ${res.status}`)
  }
  return res.json() as Promise<T>
}

export async function getSession(): Promise<SessionResponse> {
  const quizId = quizIdFromPath()
  const headers = await authHeaders()
  const data = await request<QuizDetailOut>(`/api/v1/quizzes/${quizId}`, { headers })

  const questions: QuizQuestion[] = data.questions.map((q) => ({
    id: q.question_id,
    title: q.question.length > 14 ? `${q.question.slice(0, 14)}…` : q.question,
    category: '変更内容を理解する',
    points: 10,
    question: q.question,
    choices: q.choices.map((label, i) => ({ id: CHOICE_IDS[i] ?? String(i + 1), label })),
    hint: q.hint,
  }))

  const { repository_name: _repositoryName, ...commit } = data.commit
  return {
    commit,
    quiz: {
      session_id: data.quiz_id,
      repository_id: data.commit.repository_id,
      commit_sha: data.commit.commit_sha,
      pass_policy: { require_all_correct: true },
      questions,
    },
  }
}

export async function gradeAnswer(questionId: string, choiceId: string): Promise<GradeResult> {
  const quizId = quizIdFromPath()
  const headers = await authHeaders()
  const selectedIndex = CHOICE_IDS.indexOf(choiceId)
  const result = await request<AnswerOut>(`/api/v1/quizzes/${quizId}/answers`, {
    method: 'POST',
    headers: { ...headers, 'Content-Type': 'application/json' },
    body: JSON.stringify({ question_id: questionId, selected_index: selectedIndex }),
  })
  return {
    question_id: result.question_id,
    // The server never reveals the correct choice; when the answer was
    // correct the selected choice IS the correct one, which is all the
    // highlight in QuizCard needs.
    correct: result.correct,
    correct_choice_id: result.correct ? choiceId : '',
    feedback: result.feedback,
    passed: result.passed,
  }
}

// /talk 用の API 呼び出し（backend/app/routers/talk.py）。認証は既存と同じ Supabase のセッション。
// 既存の data/api.ts には手を入れず、ここに小さく持つ。
import { supabase } from '../lib/supabase'

const API_BASE = (import.meta.env.VITE_API_BASE_URL as string | undefined) ?? 'http://localhost:8100'

export type TalkErrorKind = 'unauthenticated' | 'not_found' | 'busy' | 'unavailable' | 'failed'

export class TalkError extends Error {
  constructor(
    public kind: TalkErrorKind,
    message: string,
  ) {
    super(message)
  }
}

/** HTTP の失敗を画面向けのエラーにする。503（Claude Code が無い等）はサーバーの文言をそのまま出す。 */
export function errorFromResponse(status: number, detail: string | undefined): TalkError {
  if (status === 401) return new TalkError('unauthenticated', 'ログインが必要です')
  if (status === 404) return new TalkError('not_found', 'この commit は見つかりません')
  if (status === 409) return new TalkError('busy', detail ?? 'ぽんたはまだ前の返事を考えています')
  if (status === 503) return new TalkError('unavailable', detail ?? 'サーバーが使えません')
  return new TalkError('failed', detail ?? `HTTP ${status}`)
}

async function talkFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  if (!supabase) throw new TalkError('unauthenticated', 'Supabase が設定されていません（frontend/.env.local）')
  const { data } = await supabase.auth.getSession()
  const token = data.session?.access_token
  if (!token) throw new TalkError('unauthenticated', 'ログインが必要です')
  let res: Response
  try {
    res = await fetch(`${API_BASE}/api/v1/talk${path}`, {
      ...init,
      headers: { ...init.headers, Authorization: `Bearer ${token}` },
    })
  } catch {
    throw new TalkError('failed', `バックエンド (${API_BASE}) に接続できません`)
  }
  if (!res.ok) {
    const detail = await res
      .json()
      .then((b: { detail?: unknown }) => (typeof b.detail === 'string' ? b.detail : undefined))
      .catch(() => undefined)
    throw errorFromResponse(res.status, detail)
  }
  return res.json() as Promise<T>
}

export type TopicKey = 'intent' | 'impact' | 'improve'
export type TopicState = 'todo' | 'active' | 'done'
export type Topics = Record<TopicKey, TopicState>
export type Mood = 'happy' | 'curious' | 'thinking' | 'gentle'

export interface TalkCommitSummary {
  id: string
  commit_sha: string
  short_sha: string
  title: string
  branch: string | null
  repository_name: string
  created_at: string
  file_count: number
  quiz: { status: 'ready' | 'passed'; solved_count: number; total_count: number }
}

export interface TalkCommitDetail {
  id: string
  commit_sha: string
  short_sha: string
  message: string
  branch: string | null
  repository_name: string
  created_at: string
  files: string[]
  diff: string
}

export interface ChatReply {
  display: string
  mood: Mood
  topics: Topics
  learned: string[]
  summary: string
}

export const listCommits = () => talkFetch<{ commits: TalkCommitSummary[] }>('/commits').then((r) => r.commits)

export const getCommit = (id: string) => talkFetch<TalkCommitDetail>(`/commits/${id}`)

export const chat = (
  id: string,
  body: { messages: { role: 'ponta' | 'user'; text: string }[]; topics: Topics; learned: string[] },
) =>
  talkFetch<ChatReply>(`/commits/${id}/chat`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })

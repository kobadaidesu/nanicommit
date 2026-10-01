// 会話の保存（ブラウザの localStorage。サーバーには保存しない）。壊れた値・使えない環境では何もしない。
import type { Mood, Topics } from './api'
import { INITIAL_TOPICS, TOPICS } from './ponta'

export interface TalkMessage {
  role: 'ponta' | 'user'
  text: string
  mood?: Mood
  /** ISO 8601 の日時（表示は時:分）。 */
  at: string
}

export interface TalkSaved {
  messages: TalkMessage[]
  topics: Topics
  learned: string[]
  summary: string
}

export const storageKey = (commitId: string) => `nanicommit.talk.${commitId}`

const MOODS = ['happy', 'curious', 'thinking', 'gentle']
const STATES = ['todo', 'active', 'done']

/** 保存された値を読む。形が違えば null（最初から）。 */
export function parseSaved(raw: string | null): TalkSaved | null {
  if (!raw) return null
  let v: unknown
  try {
    v = JSON.parse(raw)
  } catch {
    return null
  }
  if (typeof v !== 'object' || v === null) return null
  const o = v as Record<string, unknown>
  if (!Array.isArray(o.messages)) return null
  const messages: TalkMessage[] = []
  for (const m of o.messages) {
    if (typeof m !== 'object' || m === null) return null
    const { role, text, mood, at } = m as Record<string, unknown>
    if ((role !== 'ponta' && role !== 'user') || typeof text !== 'string') return null
    messages.push({
      role,
      text,
      at: typeof at === 'string' ? at : new Date(0).toISOString(),
      ...(typeof mood === 'string' && MOODS.includes(mood) ? { mood: mood as Mood } : {}),
    })
  }
  const t = (typeof o.topics === 'object' && o.topics !== null ? o.topics : {}) as Record<string, unknown>
  const topics = { ...INITIAL_TOPICS }
  for (const { key } of TOPICS)
    if (typeof t[key] === 'string' && STATES.includes(t[key])) topics[key] = t[key] as Topics[typeof key]
  const learned = Array.isArray(o.learned)
    ? o.learned.filter((x): x is string => typeof x === 'string').slice(0, 5)
    : []
  return { messages, topics, learned, summary: typeof o.summary === 'string' ? o.summary : '' }
}

export function loadTalk(
  commitId: string,
  storage: Pick<Storage, 'getItem'> | undefined = safeStorage(),
): TalkSaved | null {
  try {
    return parseSaved(storage?.getItem(storageKey(commitId)) ?? null)
  } catch {
    return null
  }
}

export function saveTalk(
  commitId: string,
  saved: TalkSaved,
  storage: Pick<Storage, 'setItem'> | undefined = safeStorage(),
) {
  try {
    storage?.setItem(storageKey(commitId), JSON.stringify(saved))
  } catch {
    // 容量オーバー・プライベートモード等。保存できなくても会話は続けられる。
  }
}

export function clearTalk(commitId: string, storage: Pick<Storage, 'removeItem'> | undefined = safeStorage()) {
  try {
    storage?.removeItem(storageKey(commitId))
  } catch {
    // 同上
  }
}

function safeStorage(): Storage | undefined {
  try {
    return globalThis.localStorage
  } catch {
    return undefined
  }
}

// ぽんた（キャラクター）の見た目と、3つのテーマ。画像は public/talk/ にある。
import type { Mood, TopicKey, Topics } from './api'

export type Pose = 'sleep' | 'cheer' | 'laptop' | 'read' | 'think'

export const poseSrc = (pose: Pose) => `/talk/${pose}.png`

const MOOD_POSE: Record<Mood, Pose> = { happy: 'cheer', curious: 'read', thinking: 'think', gentle: 'read' }

/** mood → ポーズ。知らない mood は read。 */
export function poseFor(mood: string | undefined): Pose {
  return MOOD_POSE[mood as Mood] ?? 'read'
}

export const TOPICS: { key: TopicKey; label: string; check: string }[] = [
  { key: 'intent', label: '変更の意図', check: '変更の意図を説明できた' },
  { key: 'impact', label: '影響範囲', check: '影響範囲を考えられた' },
  { key: 'improve', label: '改善ポイント', check: '改善ポイントを考えられた' },
]

export const INITIAL_TOPICS: Topics = { intent: 'todo', impact: 'todo', improve: 'todo' }

export const doneCount = (topics: Topics) => TOPICS.filter((t) => topics[t.key] === 'done').length

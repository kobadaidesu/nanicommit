// /talk: 自分の commit 一覧。各行から /talk/<id> へ。
import { useCallback, useEffect, useState } from 'react'
import { listCommits } from './api'
import type { TalkCommitSummary, TalkError } from './api'
import { ErrorNotice, Logo, Notice, toTalkError } from './parts'
import { doneCount, poseSrc } from './ponta'
import { loadTalk } from './storage'

const dateLabel = (iso: string) =>
  new Date(iso).toLocaleString('ja-JP', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })

function quizBadge(q: TalkCommitSummary['quiz']) {
  if (q.status === 'passed') return <span className="talk-badge talk-ok">クイズ合格</span>
  return (
    <span className="talk-badge">
      クイズ {q.solved_count}/{q.total_count}
    </span>
  )
}

function talkBadge(id: string) {
  const saved = loadTalk(id)
  if (!saved || saved.messages.length === 0) return null
  const done = doneCount(saved.topics)
  return <span className={'talk-badge' + (done === 3 ? ' talk-ok' : '')}>ふりかえり {done}/3</span>
}

export function TalkListPage() {
  const [commits, setCommits] = useState<TalkCommitSummary[] | null>(null)
  const [error, setError] = useState<TalkError | null>(null)

  const load = useCallback(() => {
    setError(null)
    listCommits()
      .then(setCommits)
      .catch((e: unknown) => setError(toTalkError(e)))
  }, [])
  useEffect(load, [load])

  if (error) return <ErrorNotice error={error} onRetry={load} />
  if (!commits) return <Notice pose="laptop" title="commit を読み込み中…" />

  return (
    <div className="talk-page">
      <Logo />
      <header className="talk-card talk-pagehead">
        <img src={poseSrc('read')} alt="" />
        <div>
          <h1 className="talk-maru">ぽんたと commit をふりかえろう</h1>
          <p className="talk-sub">
            自分の commit を選ぶと、ぽんたが「変更の意図・影響範囲・改善ポイント」を一緒に考えてくれるよ。
            正解を選ぶクイズではなく、考えを言葉にして整理する時間です。
          </p>
        </div>
      </header>

      {commits.length === 0 ? (
        <section className="talk-card talk-notice">
          <img src={poseSrc('sleep')} alt="" />
          <h2 className="talk-maru">まだ commit がないよ</h2>
          <p className="talk-sub">commitcoach を init したリポジトリで commit すると、ここに出てくるよ。</p>
        </section>
      ) : (
        <div className="talk-list">
          {commits.map((c) => (
            <section key={c.id} className="talk-card talk-item">
              <div className="talk-item-main">
                <span className="talk-sha">{c.short_sha}</span>
                <div className="talk-item-title">{c.title || '(メッセージなし)'}</div>
                <div className="talk-item-meta">
                  <span>{c.repository_name}</span>
                  {c.branch && <span>{c.branch}</span>}
                  <span>{dateLabel(c.created_at)}</span>
                  <span>{c.file_count} ファイル</span>
                  {quizBadge(c.quiz)}
                  {talkBadge(c.id)}
                </div>
              </div>
              <a className="talk-btn" href={`/talk/${c.id}`}>
                ぽんたとふりかえる
              </a>
            </section>
          ))}
        </div>
      )}
    </div>
  )
}

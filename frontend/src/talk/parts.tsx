// /talk の小さな共通部品。
import type { ReactNode } from 'react'
import { TalkError } from './api'
import { poseSrc } from './ponta'
import type { Pose } from './ponta'
import { splitCode } from './text'

export function Logo() {
  return (
    <a className="talk-logo" href="/talk">
      <img src={poseSrc('cheer')} alt="" />
      <span className="talk-maru">nanicommit</span>
    </a>
  )
}

export function UserIcon() {
  return (
    <svg width="26" height="26" viewBox="0 0 24 24" fill="#6b8bb3" aria-hidden="true">
      <circle cx="12" cy="8" r="4" />
      <path d="M4 20c0-4 3.6-6.5 8-6.5s8 2.5 8 6.5z" />
    </svg>
  )
}

/** ぽんたの吹き出しの本文。識別子っぽい部分は <code> で見せる。 */
export function PontaText({ text }: { text: string }) {
  return (
    <>
      {splitCode(text).map((part, i) =>
        part.code ? <code key={i}>{part.text}</code> : <span key={i}>{part.text}</span>,
      )}
    </>
  )
}

/** 1ページ全体のお知らせ（読み込み中・ログインが必要・見つからない 等）。 */
export function Notice({ pose, title, children }: { pose: Pose; title: string; children?: ReactNode }) {
  return (
    <div className="talk-page">
      <Logo />
      <section className="talk-card talk-notice">
        <img src={poseSrc(pose)} alt="" />
        <h2 className="talk-maru">{title}</h2>
        {children}
      </section>
    </div>
  )
}

/** API のエラーをページ全体で見せる（一覧・詳細の読み込み失敗）。 */
export function ErrorNotice({ error, onRetry }: { error: TalkError; onRetry?: () => void }) {
  if (error.kind === 'unauthenticated') {
    return (
      <Notice pose="sleep" title="ログインが必要だよ">
        <p className="talk-sub">
          GitHub でログインすると、自分の commit をぽんたとふりかえれるよ。
          <br />
          ログインしたら、このページ（/talk）にもう一度来てね。
        </p>
        <a className="talk-btn" href="/connect">
          ログインページへ
        </a>
      </Notice>
    )
  }
  if (error.kind === 'not_found') {
    return (
      <Notice pose="sleep" title="この commit は見つからないよ">
        <p className="talk-sub">URL が正しいか、自分の commit かを確認してね（他の人の commit は開けません）。</p>
        <a className="talk-btn" href="/talk">
          commit 一覧へ
        </a>
      </Notice>
    )
  }
  return (
    <Notice pose="think" title="うまく読み込めなかったよ">
      <p className="talk-sub">{error.message}</p>
      {onRetry && (
        <button className="talk-btn" type="button" onClick={onRetry}>
          もう一度
        </button>
      )}
    </Notice>
  )
}

export const toTalkError = (e: unknown) => (e instanceof TalkError ? e : new TalkError('failed', String(e)))

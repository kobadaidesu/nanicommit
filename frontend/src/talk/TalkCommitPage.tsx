// /talk/<id>: ぽんたと1つの commit をふりかえる画面（左: commit とテーマ / 中央: 差分と会話 / 右: 理解度）。
// 会話はサーバーに保存せず、このブラウザの localStorage に保存して、再訪時に続きから話せる。
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { KeyboardEvent } from 'react'
import { parseUnifiedDiff } from '../lib/diff'
import { chat, getCommit } from './api'
import type { TalkCommitDetail, TalkError } from './api'
import { DiffPanel } from './DiffPanel'
import { ErrorNotice, Logo, Notice, PontaText, UserIcon, toTalkError } from './parts'
import { INITIAL_TOPICS, TOPICS, doneCount, poseFor, poseSrc } from './ponta'
import { fileStats } from './splitDiff'
import { clearTalk, loadTalk, saveTalk } from './storage'
import type { TalkMessage, TalkSaved } from './storage'
import { shouldSend, timeLabel } from './text'

const EMPTY: TalkSaved = { messages: [], topics: INITIAL_TOPICS, learned: [], summary: '' }

export function TalkCommitPage({ id }: { id: string }) {
  const [detail, setDetail] = useState<TalkCommitDetail | null>(null)
  const [loadError, setLoadError] = useState<TalkError | null>(null)
  const [convo, setConvo] = useState<TalkSaved>(EMPTY)
  const [busy, setBusy] = useState(false)
  const [chatError, setChatError] = useState<{ error: TalkError; greeting: boolean } | null>(null)
  const [input, setInput] = useState('')
  const [confirmReset, setConfirmReset] = useState(false)
  const [newIndex, setNewIndex] = useState(-1) // 届いたばかりの返事（少し弾ませる）
  const chatRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const started = useRef(false) // StrictMode で effect が2回走っても、あいさつを二重に頼まない

  const files = useMemo(() => (detail ? parseUnifiedDiff(detail.diff) : []), [detail])
  const stats = useMemo(() => new Map(files.map((f) => [f.displayPath, fileStats(f)])), [files])

  /** messages までの会話に対して、ぽんたの返事を頼む（messages が空ならあいさつ）。 */
  const requestReply = useCallback(
    async (base: TalkSaved, messages: TalkMessage[], restoreText?: string) => {
      setBusy(true)
      setChatError(null)
      try {
        const reply = await chat(id, {
          messages: messages.map((m) => ({ role: m.role, text: m.text })),
          topics: base.topics,
          learned: base.learned,
        })
        const next: TalkSaved = {
          messages: [
            ...messages,
            { role: 'ponta', text: reply.display, mood: reply.mood, at: new Date().toISOString() },
          ],
          topics: reply.topics,
          learned: reply.learned,
          summary: reply.summary || base.summary,
        }
        setConvo(next)
        setNewIndex(next.messages.length - 1)
        saveTalk(id, next)
      } catch (e) {
        // 送った発言は取り消して入力欄に戻す（そのまま再送できる）。
        setConvo(base)
        if (restoreText !== undefined) setInput(restoreText)
        setChatError({ error: toTalkError(e), greeting: messages.length === 0 })
      } finally {
        setBusy(false)
        inputRef.current?.focus()
      }
    },
    [id],
  )

  useEffect(() => {
    if (started.current) return
    started.current = true
    getCommit(id)
      .then((d) => {
        setDetail(d)
        const saved = loadTalk(id)
        if (saved && saved.messages.length > 0) setConvo(saved)
        else void requestReply(EMPTY, [])
      })
      .catch((e: unknown) => setLoadError(toTalkError(e)))
  }, [id, requestReply])

  useEffect(() => {
    chatRef.current?.scrollTo({ top: chatRef.current.scrollHeight, behavior: 'smooth' })
  }, [convo.messages.length, busy])

  useEffect(() => {
    const t = inputRef.current
    if (!t) return
    t.style.height = 'auto'
    t.style.height = `${Math.min(t.scrollHeight, 160)}px`
  }, [input])

  if (loadError) return <ErrorNotice error={loadError} />
  if (!detail) return <Notice pose="laptop" title="commit を読み込み中…" />

  const send = () => {
    const text = input.trim()
    if (!text || busy) return
    const messages: TalkMessage[] = [...convo.messages, { role: 'user', text, at: new Date().toISOString() }]
    setInput('')
    setConvo({ ...convo, messages })
    void requestReply(convo, messages, text)
  }

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (shouldSend({ key: e.key, shiftKey: e.shiftKey, isComposing: e.nativeEvent.isComposing, keyCode: e.keyCode })) {
      e.preventDefault()
      send()
    }
  }

  const reset = () => {
    clearTalk(id)
    setConfirmReset(false)
    setConvo(EMPTY)
    setNewIndex(-1)
    void requestReply(EMPTY, [])
  }

  const done = doneCount(convo.topics)
  const title = detail.message.trim().split('\n')[0] || '(メッセージなし)'
  const greeting = convo.messages.length === 0

  return (
    <div className="talk-app">
      {/* 左: commit と話したいポイント */}
      <aside className="talk-col">
        <Logo />
        <section className="talk-card">
          <h3 className="talk-maru">コミット</h3>
          <span className="talk-sha">{detail.short_sha}</span>
          <div className="talk-msgline" title={detail.message}>
            {title}
          </div>
          <div className="talk-meta">
            <div>
              リポジトリ <b>{detail.repository_name}</b>
            </div>
            {detail.branch && (
              <div>
                ブランチ <b>{detail.branch}</b>
              </div>
            )}
            <div>
              {detail.files.map((path) => {
                const s = stats.get(path)
                return (
                  <div key={path}>
                    <span className="talk-file">{path}</span>{' '}
                    {s ? (
                      <>
                        <span className="talk-plus">+{s.add}</span> <span className="talk-minus">-{s.del}</span>
                      </>
                    ) : (
                      <span>（差分なし）</span>
                    )}
                  </div>
                )
              })}
            </div>
          </div>
        </section>
        <section className="talk-card">
          <h3 className="talk-maru">話したいポイント</h3>
          <div className="talk-topics">
            {TOPICS.map((t) => (
              <div
                key={t.key}
                className={
                  'talk-topic' +
                  (convo.topics[t.key] === 'active'
                    ? ' talk-active'
                    : convo.topics[t.key] === 'done'
                      ? ' talk-done'
                      : '')
                }
              >
                <span className="talk-dot">{convo.topics[t.key] === 'done' ? '✓' : ''}</span>
                {t.label}
              </div>
            ))}
          </div>
        </section>
        <a className="talk-back" href="/talk">
          ← commit 一覧へ
        </a>
      </aside>

      {/* 中央: 差分と会話 */}
      <main className="talk-center">
        <div className="talk-hero">
          <h1 className="talk-maru">コミットをぽんたとふりかえろう</h1>
          <p>正解を選ぶのではなく、考えを言葉にして整理していくよ。ぽんたに説明してみよう！</p>
          <div className="talk-sticker">
            <span className="talk-maru">
              いっしょに
              <br />
              考えていこう！
            </span>
            <img src={poseSrc('read')} alt="" />
          </div>
          <div className="talk-tools">
            {confirmReset ? (
              <>
                <span className="talk-sub">会話を消して、最初からにする？</span>
                <button className="talk-chip talk-danger" type="button" onClick={reset}>
                  消してやり直す
                </button>
                <button className="talk-chip" type="button" onClick={() => setConfirmReset(false)}>
                  やめる
                </button>
              </>
            ) : (
              <button className="talk-chip" type="button" disabled={busy} onClick={() => setConfirmReset(true)}>
                ↺ 最初からやり直す
              </button>
            )}
          </div>
        </div>

        <DiffPanel files={files} />

        <div className="talk-chat" ref={chatRef} aria-live="polite">
          {convo.messages.map((m, i) =>
            m.role === 'ponta' ? (
              <div key={i} className={'talk-row talk-ponta' + (i === newIndex ? ' talk-new' : '')}>
                <div className="talk-ava">
                  <img src={poseSrc(poseFor(m.mood))} alt="ぽんた" />
                </div>
                <div className="talk-bubble">
                  <PontaText text={m.text} />
                </div>
                <span className="talk-time">{timeLabel(m.at)}</span>
              </div>
            ) : (
              <div key={i} className="talk-row talk-me">
                <div className="talk-ava talk-user">
                  <UserIcon />
                </div>
                <div className="talk-bubble">{m.text}</div>
                <span className="talk-time">{timeLabel(m.at)}</span>
              </div>
            ),
          )}
          {busy && (
            <div className="talk-row talk-ponta talk-typing">
              <div className="talk-ava">
                <img src={poseSrc(greeting ? 'laptop' : 'think')} alt="ぽんた" />
              </div>
              <div className="talk-bubble">{greeting ? 'ぽんたが commit を読んでいるよ' : 'ぽんたが考え中'}</div>
            </div>
          )}
        </div>

        {chatError && (
          <div className="talk-error" role="alert">
            <span>
              {chatError.error.kind === 'unauthenticated'
                ? 'ログインが切れたみたい。ログインし直してから、このページに戻ってきてね。'
                : chatError.error.message}
              {!chatError.greeting &&
                chatError.error.kind !== 'unauthenticated' &&
                '（送った文は入力欄に戻したよ。そのまま送り直せるよ）'}
            </span>
            {chatError.error.kind === 'unauthenticated' ? (
              <a className="talk-chip" href="/connect">
                ログインページへ
              </a>
            ) : (
              chatError.greeting && (
                <button
                  className="talk-chip"
                  type="button"
                  disabled={busy}
                  onClick={() => void requestReply(EMPTY, [])}
                >
                  もう一度あいさつしてもらう
                </button>
              )
            )}
          </div>
        )}

        <form
          className="talk-composer"
          onSubmit={(e) => {
            e.preventDefault()
            send()
          }}
        >
          <textarea
            ref={inputRef}
            rows={1}
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder="ぽんたに説明してみよう… 考えたこと、気になったこと、なんでも話してみてね！"
            aria-label="ぽんたへのメッセージ"
          />
          <button className="talk-btn" type="submit" disabled={busy || greeting || !input.trim()}>
            ➤ 送信
          </button>
        </form>
      </main>

      {/* 右: 理解度・わかったこと・今日のふりかえり */}
      <aside className="talk-col">
        <section className="talk-card">
          <div className="talk-score">
            <h3 className="talk-maru" style={{ margin: 0 }}>
              理解度
            </h3>
            <b className="talk-maru">
              {done} / {TOPICS.length}
            </b>
          </div>
          <div className="talk-bar">
            {TOPICS.map((t, i) => (
              <i key={t.key} className={i < done ? 'talk-on' : ''} />
            ))}
          </div>
          <div className="talk-checks">
            {TOPICS.map((t) => (
              <div key={t.key} className={'talk-check' + (convo.topics[t.key] === 'done' ? ' talk-done' : '')}>
                <span className="talk-dot">{convo.topics[t.key] === 'done' ? '✓' : ''}</span>
                {t.check}
              </div>
            ))}
          </div>
        </section>
        <section className="talk-card">
          <h3 className="talk-maru">会話でわかったこと</h3>
          {convo.learned.length > 0 ? (
            <ul className="talk-learned">
              {convo.learned.map((x, i) => (
                <li key={i}>{x}</li>
              ))}
            </ul>
          ) : (
            <div className="talk-empty">話していくと、ここに自分で説明できたことがたまっていくよ。</div>
          )}
        </section>
        {done === TOPICS.length && convo.summary && (
          <section className="talk-card talk-review">
            <h3 className="talk-maru">☀️ 今日のふりかえり</h3>
            <p>{convo.summary}</p>
            <img src={poseSrc('cheer')} alt="" />
          </section>
        )}
      </aside>
    </div>
  )
}

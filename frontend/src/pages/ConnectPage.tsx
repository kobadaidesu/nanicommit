// /connect: CLI とアカウントを繋ぐページ（design.md 4.1）。
// GitHub でログインし、表示された user_id を nanicommit init に渡してもらう。
import { useEffect, useState } from 'react'
import type { Session } from '@supabase/supabase-js'
import { supabase, supabaseConfigured } from '../lib/supabase'
import { GitHubIcon, LogoIcon } from '../components/Icons'
import { uiMeta } from '../data/uiMeta'

export function ConnectPage() {
  const [session, setSession] = useState<Session | null>(null)
  const [loading, setLoading] = useState(true)
  const [copied, setCopied] = useState<string | null>(null)

  useEffect(() => {
    if (!supabase) {
      setLoading(false)
      return
    }
    void supabase.auth.getSession().then(({ data }) => {
      setSession(data.session)
      setLoading(false)
    })
    const { data: sub } = supabase.auth.onAuthStateChange((_event, next) => {
      setSession(next)
    })
    return () => sub.subscription.unsubscribe()
  }, [])

  const signIn = () => {
    void supabase?.auth.signInWithOAuth({
      provider: 'github',
      options: { redirectTo: `${window.location.origin}/connect` },
    })
  }

  const signOut = () => {
    void supabase?.auth.signOut()
  }

  const copy = (label: string, text: string) => {
    void navigator.clipboard.writeText(text).then(() => {
      setCopied(label)
      setTimeout(() => setCopied(null), 1500)
    })
  }

  const userId = session?.user.id
  const initCommand = `nanicommit init --repository-id <リポジトリ登録で得たUUID> --backend-url http://localhost:8100 --user-id ${userId ?? ''}`

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50 px-4">
      <div className="w-full max-w-xl">
        <div className="mb-6 flex items-center justify-center gap-3">
          <LogoIcon className="h-10 w-10" />
          <div>
            <div className="text-xl font-bold text-gray-900">nanicommit</div>
            <div className="text-xs text-gray-500">{uiMeta.tagline}</div>
          </div>
        </div>

        <div className="rounded-xl border border-gray-200 bg-white p-8 shadow-sm">
          {!supabaseConfigured ? (
            <p className="text-sm text-gray-600">
              Supabase が設定されていません。frontend/.env.local に VITE_SUPABASE_URL と
              VITE_SUPABASE_ANON_KEY を設定してください。
            </p>
          ) : loading ? (
            <p className="text-center text-sm text-gray-500">読み込み中…</p>
          ) : !session ? (
            <>
              <h1 className="text-lg font-bold text-gray-900">CLI とアカウントを接続</h1>
              <p className="mt-2 text-sm leading-relaxed text-gray-600">
                GitHub でログインすると、あなたの user_id が表示されます。
                それを手元の nanicommit init に渡すと、commit ごとの問題生成と push
                前の合格チェックが有効になります。
              </p>
              <button
                onClick={signIn}
                className="mt-6 flex w-full items-center justify-center gap-2 rounded-lg bg-gray-900 px-4 py-2.5 text-sm font-semibold text-white hover:bg-gray-700"
              >
                <GitHubIcon className="h-5 w-5" />
                GitHub でログイン
              </button>
            </>
          ) : (
            <>
              <div className="flex items-start justify-between">
                <div>
                  <h1 className="text-lg font-bold text-gray-900">接続の準備ができました</h1>
                  <p className="mt-1 text-sm text-gray-600">
                    {session.user.user_metadata?.user_name ?? session.user.email} としてログイン中
                  </p>
                </div>
                <button onClick={signOut} className="text-sm text-gray-500 underline hover:text-gray-700">
                  ログアウト
                </button>
              </div>

              <div className="mt-6">
                <div className="text-xs font-semibold uppercase tracking-wide text-gray-500">あなたの user_id</div>
                <div className="mt-1.5 flex items-center gap-2">
                  <code className="flex-1 rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 font-mono text-sm text-gray-900">
                    {userId}
                  </code>
                  <button
                    onClick={() => copy('id', userId!)}
                    className="rounded-lg border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
                  >
                    {copied === 'id' ? 'コピーしました' : 'コピー'}
                  </button>
                </div>
              </div>

              <div className="mt-5">
                <div className="text-xs font-semibold uppercase tracking-wide text-gray-500">
                  リポジトリで実行するコマンド
                </div>
                <div className="mt-1.5 flex items-start gap-2">
                  <code className="flex-1 whitespace-pre-wrap break-all rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 font-mono text-xs leading-relaxed text-gray-900">
                    {initCommand}
                  </code>
                  <button
                    onClick={() => copy('cmd', initCommand)}
                    className="rounded-lg border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
                  >
                    {copied === 'cmd' ? 'コピーしました' : 'コピー'}
                  </button>
                </div>
                <p className="mt-2 text-xs leading-relaxed text-gray-500">
                  user_id は本人確認に使われます。他人に共有しないでください。
                </p>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  )
}

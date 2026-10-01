// /talk 画面の入口。main.tsx から React.lazy で読み込まれるので、このファイル以下（talk.css・フォント含む）は
// 別チャンクになり、既存の画面（/quizzes, /connect）には読み込まれない。
import './talk.css'
import { TalkCommitPage } from './TalkCommitPage'
import { TalkListPage } from './TalkListPage'
import { Notice } from './parts'
import { parseTalkPath } from './route'

export default function TalkApp() {
  const route = parseTalkPath(window.location.pathname)
  return (
    <div className="talk-root">
      {route.page === 'list' ? (
        <TalkListPage />
      ) : route.page === 'commit' ? (
        <TalkCommitPage id={route.id} />
      ) : (
        <Notice pose="sleep" title="ページが見つからないよ">
          <a className="talk-btn" href="/talk">
            commit 一覧へ
          </a>
        </Notice>
      )}
    </div>
  )
}

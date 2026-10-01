import { StrictMode, Suspense, lazy } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App'
import { ConnectPage } from './pages/ConnectPage'
import { isTalkPath } from './talk/route'

// /talk（ぽんたとふりかえる画面）は別チャンク。既存画面には talk の JS・CSS・フォントを読み込まない。
const TalkApp = lazy(() => import('./talk/TalkApp'))

// ルーターを入れるほどページが無いので、パスで出し分ける。
const path = window.location.pathname
const page = isTalkPath(path) ? (
  <Suspense fallback={null}>
    <TalkApp />
  </Suspense>
) : path.startsWith('/connect') ? (
  <ConnectPage />
) : (
  <App />
)

createRoot(document.getElementById('root')!).render(<StrictMode>{page}</StrictMode>)

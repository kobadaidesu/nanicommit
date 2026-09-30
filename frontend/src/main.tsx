import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App'
import { ConnectPage } from './pages/ConnectPage'

// ルーターを入れるほどページが無いので、パスで出し分ける。
const page = window.location.pathname.startsWith('/connect') ? <ConnectPage /> : <App />

createRoot(document.getElementById('root')!).render(<StrictMode>{page}</StrictMode>)

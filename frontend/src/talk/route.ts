// /talk の出し分け。main.tsx から使うのはこのファイルだけ（画面本体は TalkApp を遅延読み込みする）。

export type TalkRoute = { page: 'list' } | { page: 'commit'; id: string } | { page: 'unknown' }

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

/** /talk と /talk/... だけ（/talking などは含めない）。 */
export function isTalkPath(path: string): boolean {
  return /^\/talk(\/|$)/.test(path)
}

export function parseTalkPath(path: string): TalkRoute {
  const rest = path.replace(/^\/talk/, '').replace(/\/+$/, '')
  if (rest === '') return { page: 'list' }
  const id = rest.slice(1)
  if (rest.startsWith('/') && UUID_RE.test(id)) return { page: 'commit', id: id.toLowerCase() }
  return { page: 'unknown' }
}

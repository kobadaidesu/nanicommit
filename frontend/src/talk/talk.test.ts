import { describe, expect, it } from 'vitest'
import { parseUnifiedDiff } from '../lib/diff'
import { errorFromResponse } from './api'
import { INITIAL_TOPICS, poseFor } from './ponta'
import { isTalkPath, parseTalkPath } from './route'
import { buildSplitRows, fileStats } from './splitDiff'
import { clearTalk, loadTalk, parseSaved, saveTalk, storageKey } from './storage'
import { shouldSend, splitCode } from './text'

const ID = 'f4622c0f-655a-44f6-9927-79321865044a'

describe('パス判定', () => {
  it.each([
    ['/talk', true],
    ['/talk/', true],
    [`/talk/${ID}`, true],
    ['/talking', false],
    ['/quizzes/x', false],
    ['/connect', false],
    ['/', false],
  ])('isTalkPath(%s) = %s', (path, expected) => {
    expect(isTalkPath(path)).toBe(expected)
  })

  it('parseTalkPath', () => {
    expect(parseTalkPath('/talk')).toEqual({ page: 'list' })
    expect(parseTalkPath('/talk/')).toEqual({ page: 'list' })
    expect(parseTalkPath(`/talk/${ID.toUpperCase()}/`)).toEqual({ page: 'commit', id: ID })
    expect(parseTalkPath('/talk/not-a-uuid')).toEqual({ page: 'unknown' })
    expect(parseTalkPath(`/talk/${ID}/extra`)).toEqual({ page: 'unknown' })
  })
})

describe('差分の左右分割', () => {
  const diff = [
    'diff --git a/user_service.py b/user_service.py',
    '--- a/user_service.py',
    '+++ b/user_service.py',
    '@@ -9,4 +9,5 @@ class UserService:',
    '     a = 1',
    '-    b = 2',
    '-    c = 3',
    '+    b = 20',
    '+    c = 30',
    '+    d = 40',
    '     e = 5',
    '-    f = 6',
    '',
  ].join('\n')
  const [file] = parseUnifiedDiff(diff)

  it('削除と追加を横に並べ、足りない側は空行で埋める', () => {
    const rows = buildSplitRows(file)
    expect(rows[0]).toEqual({ type: 'hunk', left: '@@ -9,4 @@ class UserService:', right: '@@ +9,5 @@' })
    const lines = rows
      .slice(1)
      .map((r) =>
        r.type === 'line' ? [`${r.left.kind}:${r.left.no ?? ''}`, `${r.right.kind}:${r.right.no ?? ''}`] : null,
      )
    expect(lines).toEqual([
      ['ctx:9', 'ctx:9'],
      ['del:10', 'add:10'],
      ['del:11', 'add:11'],
      ['blank:', 'add:12'],
      ['ctx:12', 'ctx:13'],
      ['del:13', 'blank:'],
    ])
  })

  it('追加・削除の行数', () => {
    expect(fileStats(file)).toEqual({ add: 3, del: 3 })
  })

  it('追加だけのファイル（新規）は左が空行', () => {
    const [added] = parseUnifiedDiff(
      'diff --git a/n.py b/n.py\nnew file mode 100644\n--- /dev/null\n+++ b/n.py\n@@ -0,0 +1,2 @@\n+x\n+y\n',
    )
    const rows = buildSplitRows(added).filter((r) => r.type === 'line')
    expect(rows).toHaveLength(2)
    expect(rows.every((r) => r.type === 'line' && r.left.kind === 'blank' && r.right.kind === 'add')).toBe(true)
  })
})

describe('mood → ポーズ', () => {
  it.each([
    ['happy', 'cheer'],
    ['curious', 'read'],
    ['gentle', 'read'],
    ['thinking', 'think'],
    ['angry', 'read'],
    [undefined, 'read'],
  ])('%s → %s', (mood, pose) => {
    expect(poseFor(mood)).toBe(pose)
  })
})

describe('会話の保存（localStorage）', () => {
  const saved = {
    messages: [
      { role: 'ponta' as const, text: 'やっほー', mood: 'curious' as const, at: '2026-10-01T00:00:00.000Z' },
      { role: 'user' as const, text: '古い値を返さないため', at: '2026-10-01T00:01:00.000Z' },
    ],
    topics: { intent: 'done' as const, impact: 'active' as const, improve: 'todo' as const },
    learned: ['古い値を返さない'],
    summary: '',
  }

  it('保存して読み戻せる', () => {
    const store = new Map<string, string>()
    const storage = {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => void store.set(k, v),
      removeItem: (k: string) => void store.delete(k),
    }
    saveTalk(ID, saved, storage)
    expect([...store.keys()]).toEqual([`nanicommit.talk.${ID}`])
    expect(loadTalk(ID, storage)).toEqual(saved)
    clearTalk(ID, storage)
    expect(loadTalk(ID, storage)).toBeNull()
  })

  it('壊れた値は無視する（最初から）', () => {
    expect(parseSaved(null)).toBeNull()
    expect(parseSaved('{not json')).toBeNull()
    expect(parseSaved('"text"')).toBeNull()
    expect(parseSaved('{"messages": "x"}')).toBeNull()
    expect(parseSaved('{"messages": [{"role": "system", "text": "x"}]}')).toBeNull()
    expect(parseSaved('{"messages": [{"role": "user", "text": 1}]}')).toBeNull()
  })

  it('一部だけおかしい値は直して使う', () => {
    const raw = JSON.stringify({
      messages: [{ role: 'ponta', text: 'hi', mood: 'angry' }],
      topics: { intent: 'done', impact: 'hacked' },
      learned: ['a', 1, 'b', 'c', 'd', 'e', 'f'],
      summary: 3,
    })
    expect(parseSaved(raw)).toEqual({
      messages: [{ role: 'ponta', text: 'hi', at: '1970-01-01T00:00:00.000Z' }],
      topics: { ...INITIAL_TOPICS, intent: 'done' },
      learned: ['a', 'b', 'c', 'd', 'e'],
      summary: '',
    })
  })

  it('localStorage が使えない（例外を投げる）環境でも落ちない', () => {
    const broken = {
      getItem: () => {
        throw new Error('SecurityError')
      },
      setItem: () => {
        throw new Error('QuotaExceededError')
      },
      removeItem: () => {
        throw new Error('SecurityError')
      },
    }
    expect(loadTalk(ID, broken)).toBeNull()
    expect(() => saveTalk(ID, saved, broken)).not.toThrow()
    expect(() => clearTalk(ID, broken)).not.toThrow()
    expect(storageKey(ID)).toBe(`nanicommit.talk.${ID}`)
  })
})

describe('入力と表示', () => {
  it('Enter で送信、Shift+Enter と日本語の変換中は送らない', () => {
    const key = { key: 'Enter', shiftKey: false, isComposing: false, keyCode: 13 }
    expect(shouldSend(key)).toBe(true)
    expect(shouldSend({ ...key, shiftKey: true })).toBe(false)
    expect(shouldSend({ ...key, isComposing: true })).toBe(false)
    expect(shouldSend({ ...key, keyCode: 229 })).toBe(false) // Safari の変換確定
    expect(shouldSend({ ...key, key: 'a' })).toBe(false)
  })

  it('吹き出しの識別子を code として分ける', () => {
    expect(splitCode('update_user で self.cache.delete(user_id) を呼ぶ')).toEqual([
      { code: true, text: 'update_user' },
      { code: false, text: ' で ' },
      { code: true, text: 'self.cache.delete(user_id)' },
      { code: false, text: ' を呼ぶ' },
    ])
    expect(splitCode('日本語だけ')).toEqual([{ code: false, text: '日本語だけ' }])
  })

  it('API のエラー: 503 はサーバーの文言をそのまま', () => {
    const msg = 'Claude Code（claude コマンド）が見つかりません。インストールしてログインしてください'
    expect(errorFromResponse(503, msg)).toMatchObject({ kind: 'unavailable', message: msg })
    expect(errorFromResponse(401, 'x').kind).toBe('unauthenticated')
    expect(errorFromResponse(404, undefined).kind).toBe('not_found')
    expect(errorFromResponse(409, 'busy').kind).toBe('busy')
    expect(errorFromResponse(502, 'ぽんたの返事を作れませんでした')).toMatchObject({
      kind: 'failed',
      message: 'ぽんたの返事を作れませんでした',
    })
  })
})

// 会話の表示・入力まわりの小さな純粋関数。

/** ぽんたの吹き出しで、識別子・関数呼び出しっぽい部分を <code> で見せるための分割（HTML は組み立てない）。 */
export function splitCode(text: string): { code: boolean; text: string }[] {
  const out: { code: boolean; text: string }[] = []
  const re = /[A-Za-z_][\w.]*(?:\([^()\n]*\))?/g
  let last = 0
  for (const m of text.matchAll(re)) {
    const start = m.index ?? 0
    if (start > last) out.push({ code: false, text: text.slice(last, start) })
    out.push({ code: true, text: m[0] })
    last = start + m[0].length
  }
  if (last < text.length) out.push({ code: false, text: text.slice(last) })
  return out
}

/** Enter で送信、Shift+Enter は改行。日本語の変換確定中（isComposing / keyCode 229）は送らない。 */
export function shouldSend(e: { key: string; shiftKey: boolean; isComposing: boolean; keyCode: number }): boolean {
  return e.key === 'Enter' && !e.shiftKey && !e.isComposing && e.keyCode !== 229
}

export const timeLabel = (iso: string) =>
  new Date(iso).toLocaleTimeString('ja-JP', { hour: '2-digit', minute: '2-digit' })

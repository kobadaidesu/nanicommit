// Supabase クライアント（Web の認証と、今後の API 呼び出しのトークン取得に使う）。
// 値は VITE_SUPABASE_URL / VITE_SUPABASE_ANON_KEY（.env.local）から読む。
// anon key は公開前提のキーだが、環境ごとに差し替えるためリポジトリには入れない。
import { createClient } from '@supabase/supabase-js'

const url = import.meta.env.VITE_SUPABASE_URL as string | undefined
const anonKey = import.meta.env.VITE_SUPABASE_ANON_KEY as string | undefined

export const supabaseConfigured = Boolean(url && anonKey)

export const supabase = supabaseConfigured ? createClient(url!, anonKey!) : null

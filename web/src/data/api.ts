// Thin data-access layer. Today it serves local mock data; the backend swap
// is: make these two functions fetch() the real endpoints with the same
// response shapes (types/quiz.ts).
import type { CommitPayload } from '../types/payload'
import type { GradeResult, QuizSession, SessionResponse } from '../types/quiz'
import payloadJson from './commitPayload.json'

// Verbatim copy of examples/commit.json — a real payload the CLI produced.
const commit = payloadJson satisfies CommitPayload

const quiz: QuizSession = {
  session_id: 'mock-session-0001',
  repository_id: commit.repository_id,
  commit_sha: commit.commit_sha,
  pass_policy: { require_all_correct: true },
  questions: [
    {
      id: 'q1',
      title: 'キャッシュ無効化の理由',
      category: '実装の意図を理解する',
      points: 10,
      question:
        'このコミットで、ユーザー削除後にキャッシュを無効化する処理を追加した主な理由は何ですか?',
      choices: [
        {
          id: 'A',
          label:
            'ユーザー削除後に、古いキャッシュが残ることで不整合なデータが表示されるのを防ぐため',
        },
        { id: 'B', label: 'キャッシュの容量を節約するため' },
        { id: 'C', label: 'データベースの負荷を下げるため' },
        { id: 'D', label: 'ログの出力を増やしてデバッグしやすくするため' },
      ],
      hint: 'キャッシュを使うことでデータの読み込みは高速になりますが、更新時にはどのような問題が発生する可能性があるか考えてみましょう。',
    },
    {
      id: 'q2',
      title: '実装方法について',
      category: '実装を読み解く',
      points: 10,
      question: 'get() メソッドはどのような戦略でキャッシュを利用していますか?',
      choices: [
        { id: 'A', label: '起動時に全ユーザーを読み込んでおく(プリロード)' },
        {
          id: 'B',
          label:
            '取得時にキャッシュを確認し、なければリポジトリから読んでキャッシュに保存する(読み込み時キャッシュ)',
        },
        { id: 'C', label: '一定時間ごとにキャッシュを作り直す(定期リフレッシュ)' },
        { id: 'D', label: '書き込みのたびに必ずキャッシュを更新する(ライトスルー)' },
      ],
      hint: 'get() の中で self.cache をどの順番で参照・更新しているかに注目しましょう。',
    },
    {
      id: 'q3',
      title: '影響範囲の確認',
      category: '変更の影響を考える',
      points: 10,
      question:
        'このコミットで削除された src/legacy.py の legacy_lookup を呼び出すコードがまだ残っていた場合、何が起きますか?',
      choices: [
        { id: 'A', label: '何も起きない(自動的に無視される)' },
        { id: 'B', label: '呼び出した時点で ImportError などの実行時エラーになる' },
        { id: 'C', label: '自動的に新しいキャッシュ経由の実装が使われる' },
        { id: 'D', label: 'ビルド時にコンパイルエラーとして検出される' },
      ],
      hint: 'Python がモジュールをいつ解決するか(コンパイル時か実行時か)を考えてみましょう。',
    },
  ],
}

// Private to the mock: a real backend grades server-side and never ships this.
const ANSWER_KEY: Record<string, string> = { q1: 'A', q2: 'B', q3: 'B' }

export async function getSession(): Promise<SessionResponse> {
  return { commit, quiz }
}

export async function gradeAnswer(questionId: string, choiceId: string): Promise<GradeResult> {
  const correctChoiceId = ANSWER_KEY[questionId]
  return {
    question_id: questionId,
    correct: choiceId === correctChoiceId,
    correct_choice_id: correctChoiceId,
  }
}

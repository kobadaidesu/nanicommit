import { uiMeta } from '../data/uiMeta'
import { BranchIcon, ChevronDownIcon, ChevronRightIcon, GitHubIcon, LogoIcon } from './Icons'

interface Props {
  branch: string | null
  unlocked: boolean
}

export function TopBar({ branch, unlocked }: Props) {
  return (
    <header className="sticky top-0 z-10 border-b border-gray-200 bg-white">
      <div className="mx-auto flex max-w-[1440px] items-center gap-6 px-6 py-3">
        <div className="flex items-center gap-3">
          <LogoIcon className="h-9 w-9" />
          <div>
            <div className="text-lg font-bold leading-tight text-gray-900">CommitCoach</div>
            <div className="text-xs text-gray-500">{uiMeta.tagline}</div>
          </div>
        </div>

        <div className="flex flex-1 items-center justify-center gap-2 text-sm">
          <GitHubIcon className="h-6 w-6 text-gray-900" />
          <span className="text-gray-500">{uiMeta.org}</span>
          <span className="text-gray-400">/</span>
          <span className="font-bold text-gray-900">{uiMeta.repo}</span>
          <ChevronRightIcon className="h-4 w-4 text-gray-400" />
          <span className="flex items-center gap-1 rounded-md border border-blue-200 bg-blue-50 px-2.5 py-1 font-medium text-blue-700">
            <BranchIcon className="h-3.5 w-3.5" />
            {branch ?? 'detached HEAD'}
            <ChevronRightIcon className="h-3.5 w-3.5" />
          </span>
        </div>

        <div className="flex items-center gap-6">
          {unlocked ? (
            <div className="text-right">
              <div className="rounded-lg bg-green-50 px-4 py-1.5 text-sm font-semibold text-green-700">
                <span className="mr-1.5">●</span>push が許可されました
              </div>
              <div className="mt-0.5 text-xs text-gray-500">git push を実行できます</div>
            </div>
          ) : (
            <div className="text-right">
              <div className="rounded-lg bg-red-50 px-4 py-1.5 text-sm font-semibold text-red-600">
                <span className="mr-1.5">●</span>push はまだ許可されていません
              </div>
              <div className="mt-0.5 text-xs text-gray-500">すべての問題に正解すると push できます</div>
            </div>
          )}
          <button className="flex items-center gap-2">
            <span className="flex h-8 w-8 items-center justify-center rounded-full bg-blue-100 text-sm font-bold text-blue-700">
              {uiMeta.user.charAt(0).toUpperCase()}
            </span>
            <span className="text-sm font-medium text-gray-800">{uiMeta.user}</span>
            <ChevronDownIcon className="h-4 w-4 text-gray-500" />
          </button>
        </div>
      </div>
    </header>
  )
}

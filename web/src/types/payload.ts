// CommitPayload mirrors docs/commit-payload.schema.json — the six-field JSON
// that the commitcoach CLI records for each commit. Keep in sync with the CLI.
export interface CommitPayload {
  repository_id: string
  commit_sha: string
  /** null when HEAD was detached at commit time. */
  branch: string | null
  message: string
  /** Repository-relative paths the diff covers, in diff order. */
  files: string[]
  /** Concatenated unified diff (git diff-tree --patch output). */
  diff: string
}

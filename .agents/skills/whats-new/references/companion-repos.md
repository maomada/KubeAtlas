# Optional companion documentation

The authoritative repository is `maomada/KubeAtlas`; its documentation lives in
`docs/`. No separate documentation or marketing repository is configured here.

Only inspect or edit a companion repository when the user includes it in scope
and its remote has been verified from an existing checkout or explicit project
configuration. Do not infer remote URLs, directory names, publishing commands or
sync mappings from this project's product name.

Before working in a selected companion repository:

1. Resolve the actual Git root and remotes, and read its applicable instructions.
2. Inspect tracked/untracked changes, branches, worktrees and in-progress Git
   operations. Preserve existing work and local-only commits.
3. Use a clean isolated worktree when the current checkout cannot safely be used.
   Record the owning repository, absolute worktree path and branch.
4. Read that repository's documented synchronization or changelog workflow. Edit
   source documents rather than generated copies, and verify navigation changes.

Use the same merged-work inventory as the in-app notes: capabilities, defaults,
configuration, prerequisites and limits. An audit request authorizes a report;
editing, publishing and merging require their own scope. Do not claim unshipped
features are live or assign an old feature the date of a later cosmetic change.

At completion, preserve intended edits on the task branch and report each
selected repository and validation result. Remove only clean temporary worktrees
created for this task, after verifying their exact real paths. Never force
removal or remove an existing checkout. Report any cleanup that remains blocked.

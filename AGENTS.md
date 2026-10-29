# Repository workflow

- After completing and verifying a user-requested code, documentation, WebUI, backend, or repository-managed skill change, commit it and push it to the configured private remote unless the user explicitly says not to for that change.
- Stage only files owned by the current task. Preserve unrelated modified and untracked files.
- Run tests appropriate to the changed area and `git diff --check` before committing. After pushing, verify that the remote branch resolves to the local commit.
- Never commit account state, passwords, OTPs, cookies, tokens, DSIDs, PETs, authenticated responses, logs, generated binaries, or other sensitive/runtime artifacts.

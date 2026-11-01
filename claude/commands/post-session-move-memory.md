Reconcile project memory after a Claude session has been moved between
project namespaces with `ccctl move <session-id> <dir>`
(https://github.com/crux/ccctl). Each Claude project has its own
memory namespace at `~/.claude/projects/<encoded-project-path>/memory/`,
with files referenced from a `MEMORY.md` index. When a session is moved
to a different project namespace, any memory files it wrote during the
prior context remain stranded in the **old** namespace — invisible to the
resumed session and polluting the old project's memory directory.

This command identifies those stranded memory writes and walks the user
through deciding what to do with each.

## 1. Find the current session's .jsonl

The current project namespace encodes the working directory path:
characters `/`, `.`, ` `, `@` are each replaced by `-`. Example:
`/Users/me/src/app` → `~/.claude/projects/-Users-me-src-app/`.

```bash
encoded=$(realpath "$PWD" | sed 's![/. @]!-!g')
project_dir="$HOME/.claude/projects/$encoded"
session_file=$(ls -t "$project_dir"/*.jsonl 2>/dev/null | head -1)
```

The newest `.jsonl` in that directory is the current session.

## 2. Scan it for stranded memory writes

Extract `Write`/`Edit` tool-call file paths and filter to memory paths
NOT in the current namespace:

```bash
jq -r '
  select(.type == "assistant")
  | .message.content[]?
  | select(.type == "tool_use" and (.name == "Write" or .name == "Edit"))
  | .input.file_path // empty
' "$session_file" \
  | grep -E "$HOME/.claude/projects/[^/]+/memory/" \
  | grep -v "^$project_dir/memory/" \
  | sort -u
```

Then drop paths whose files are no longer present on disk — files that
were `Write`-ten and later removed in the same session show up in the
scan but require no further action:

```bash
... | while IFS= read -r p; do [[ -f "$p" ]] && printf '%s\n' "$p"; done
```

## 3. No-op cleanly when nothing is found

If the filter yields no results, respond with a single sentence:
"No memory files were written outside the current project's namespace
during this session — nothing to reconcile."

End. Do not search further. Do not investigate adjacent cleanup. Do not
offer related tasks.

## 4. Multi-project session check

If the surviving paths span more than one foreign project namespace
(e.g. some under `<old-A>/memory/` and others under `<old-B>/memory/`),
this is likely a session that legitimately operated on multiple
projects' memories rather than a single move that left everything
behind. Detect it:

```bash
echo "$paths" \
  | sed -E "s|^$HOME/.claude/projects/([^/]+)/.*|\\1|" \
  | sort -u
```

If the count is greater than 1, surface this to the user before walking
through any file:

> This session wrote memory across N different project namespaces. That
> suggests the session worked on more than one project's memories rather
> than a single move-and-strand. I'll walk through each file
> individually; please review each before applying — the default
> disposition is **leave** when content matches its containing
> namespace.

Then proceed to the per-file walk-through (next section), but bias
toward **leave** unless the file's content clearly indicates it belongs
in the current namespace rather than where it sits.

## 5. Otherwise, walk through each stranded file

For each file:

1. **Read it.** Look at YAML frontmatter `type:` and content body.

2. **Recommend a default disposition** based on type:

   | Type       | Default disposition                                                  |
   |------------|----------------------------------------------------------------------|
   | user       | Relocate — user information is not project-specific                  |
   | feedback   | Relocate — user-corrections/preferences typically apply generally    |
   | reference  | Relocate — pointers to external systems are usually generic          |
   | project    | Leave or remove — project memories are usually scoped to the source |

   Default is a starting point, not a rule. A `project` memory whose
   content reads as user-level guidance may belong in the new namespace;
   a `user`/`feedback` memory rooted in a specific project's situation
   may not transfer cleanly. When heuristic and content conflict, ask.

3. **Show the user enough to decide.** Quote the frontmatter and a short
   excerpt of the body in the question. Offer choices: **move / leave /
   remove**, plus **show full content** for borderline cases.

4. **Apply the decision:**
   - **Move** — read the file from the old path; write it to the
     corresponding path under the current project's `memory/`; remove
     the original (after explicit user confirmation in chat); edit the
     OLD `MEMORY.md` to drop the index entry; edit the NEW `MEMORY.md`
     to add a corresponding entry, matching the wording style of
     existing entries there.
   - **Remove** — delete the file (after explicit user confirmation);
     remove the index entry from the OLD `MEMORY.md`.
   - **Leave** — take no action.

## 6. Final report

Summarize concisely: how many files were moved, left, removed; which
`MEMORY.md` indexes were edited. The user can scroll up for detail.

## Constraints

- Do not touch memory files the session did not write (the scan
  intentionally excludes those — files in the old namespace predating
  the moved session are unrelated).
- Do not edit anything outside `~/.claude/projects/<...>/memory/`.
- Do not assume the user wants broader cleanup; this command is scoped
  to *this session's stranded writes*, nothing more.
- For any `rm`, confirm with the user in chat first. Destructive
  operations on memory should be visible step by step, even though
  the command is user-invoked.

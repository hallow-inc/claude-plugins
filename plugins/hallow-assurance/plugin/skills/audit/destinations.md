# Audit destinations

A destination receives documented findings. `assure` never files anything; the destination is a
file or a command the human configured, outside the committed manifest, so an audit of someone
else's repository leaves no trace upstream.

## Resolve the destination

Take the first that exists:

1. `.claude/hallow-assurance.local.md` at the repository root: the `audit.destination` key in its
   YAML frontmatter. The repository must not track this file; when `git ls-files` lists it, stop
   and tell the human, because a committed command would run for everyone who audits.
2. `$HOME/.config/hallow-assurance/audit.yaml`: the `audit.destination` key.
3. Neither: a `file` destination at `.assure/state/audit/documented.md`.

`audit.destination` is one map:

| `type` | Fields |
|---|---|
| `file` | `path`: a Markdown file, relative to the repository root |
| `command` | `command`: run with `sh -c`, the finding JSON on stdin, exit 0 when sent. `lookup`: run with `sh -c` and `ASSURE_FINDING_ID` set, exit 0 when the destination already holds the finding, 1 when it does not |

A `command` destination without `lookup`, or any other `type`, is a configuration error: report it
and send nothing.

## Send each documented finding

Extract one finding at a time from the findings file:

```sh
jq -c --arg id "$ID" '.findings[] | select(.id == $id)' .assure/state/audit/findings.json
```

**file**: skip the finding when `grep -qF "## $ID " "$path"` succeeds; otherwise append it:

```sh
mkdir -p "$(dirname "$path")"
jq -r '"## \(.id) \(.kind) \(.path)\(if .line then ":\(.line)" else "" end)\n\n- level: \(.level)\n- detail: \(.detail)\n"' >> "$path"
```

**command**: run `lookup` with `ASSURE_FINDING_ID="$ID"`. Exit 0: skip, already held. Exit 1:
pipe the finding JSON to `command`. Any other exit from `lookup`, or a non-zero exit from
`command`: stop sending, and report the finding and the command's output.

The step is done when every documented finding was sent or skipped as already held. Report each
one's outcome.

## Command recipes

Each recipe writes `assure-finding: <id>` into the issue body; its `lookup` searches for that id.

**GitHub issues** (personal projects), using an authenticated `gh` in the repository:

```yaml
audit:
  destination:
    type: command
    command: |
      f=$(cat)
      gh issue create \
        --title "$(printf %s "$f" | jq -r '"audit: \(.kind) in \(.path)"')" \
        --body "$(printf %s "$f" | jq -r '"```json\n\(.)\n```\n\nassure-finding: \(.id)"')"
    lookup: |
      n=$(gh issue list --state all --search "$ASSURE_FINDING_ID in:body" --json number --jq length) || exit 2
      [ "$n" -gt 0 ]
```

GitHub's search index lags issue creation by seconds to minutes, so a re-audit run immediately
after documenting can file a duplicate.

**Linear** (hallow-inc work), with `LINEAR_API_KEY` and `LINEAR_TEAM_ID` exported:

```yaml
audit:
  destination:
    type: command
    command: |
      jq -Rs --arg team "$LINEAR_TEAM_ID" '. as $f | ($f | fromjson) as $x | {
          query: "mutation($i: IssueCreateInput!) { issueCreate(input: $i) { success } }",
          variables: {i: {teamId: $team, title: "audit: \($x.kind) in \($x.path)",
            description: "```json\n\($f)\n```\n\nassure-finding: \($x.id)"}}}' |
        curl -sf https://api.linear.app/graphql -H "Authorization: $LINEAR_API_KEY" \
          -H 'Content-Type: application/json' -d @- |
        jq -e '.data.issueCreate.success' > /dev/null
    lookup: |
      n=$(jq -n --arg id "$ASSURE_FINDING_ID" '{
          query: "query($id: String!) { issues(filter: {description: {contains: $id}}) { nodes { id } } }",
          variables: {id: $id}}' |
        curl -sf https://api.linear.app/graphql -H "Authorization: $LINEAR_API_KEY" \
          -H 'Content-Type: application/json' -d @- |
        jq -e '.data.issues.nodes | length') || exit 2
      [ "$n" -gt 0 ]
```

In `.claude/hallow-assurance.local.md`, put the same YAML between `---` lines at the top of the
file.

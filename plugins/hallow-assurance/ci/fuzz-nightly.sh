#!/usr/bin/env bash
set -euo pipefail

fuzztime=${FUZZTIME:-60s}
run_url=${RUN_URL:-}
label=fuzz-crasher
max_inline=60000

status=0
label_ensured=0

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

prefix=$(git rev-parse --show-prefix)
module=$(go list -m -f '{{.Path}}')
packages=$(go list ./...)

snapshot() {
  local dir=$1 out=$2
  if [[ -d $dir ]]; then
    find "$dir" -mindepth 1 -maxdepth 1 -type f -exec basename {} \; | LC_ALL=C sort >"$out"
  else
    : >"$out"
  fi
}

longest_backtick_run() {
  awk '{ s = $0; while (match(s, /`+/)) { if (RLENGTH > m) m = RLENGTH; s = substr(s, RSTART + RLENGTH) } } END { print m + 0 }' "$1"
}

write_body() {
  local out=$1 pkg=$2 target=$3 hash=$4 file=$5 issue_url=$6
  local size fence n corpus where
  size=$(wc -c <"$file")
  size=$((size))
  corpus="${prefix}${pkg}/testdata/fuzz/${target}/${hash}"
  if [[ $pkg == "." ]]; then
    corpus="${prefix}testdata/fuzz/${target}/${hash}"
  fi
  if [[ -n $prefix ]]; then
    where="\`${prefix%/}\`"
  else
    where="the repository root"
  fi

  {
    printf '`%s` in `%s` wrote a new failing input.\n\n' "$target" "$pkg"
    if [[ -n $run_url ]]; then
      printf 'Run: %s\n\n' "$run_url"
    fi
    if ((size > max_inline)); then
      printf 'The input is %d bytes, too large to include here. Download it from the `fuzz-corpus` artifact' "$size"
      if [[ -n $run_url ]]; then
        printf ' of %s' "$run_url"
      else
        printf ' of the nightly fuzz run'
      fi
      printf '.\n\n'
    else
      n=$(longest_backtick_run "$file")
      if ((n < 2)); then n=2; fi
      fence=$(printf '%*s' "$((n + 1))" '' | tr ' ' '`')
      printf 'Input:\n\n%s\n' "$fence"
      cat "$file"
      if [[ -n $(tail -c1 "$file") ]]; then
        printf '\n'
      fi
      printf '%s\n\n' "$fence"
    fi
    printf 'Re-run it from %s:\n\n```\ngo test -run='"'"'^%s$/^%s$'"'"' ./%s\n```\n\n' "$where" "$target" "$hash" "$pkg"
    printf 'Commit the input at `%s`.\n\n' "$corpus"
    if [[ -n $issue_url ]]; then
      printf 'To fix it, run `/hallow-assurance:bugfix %s`.\n' "$issue_url"
    else
      printf "To fix it, run \`/hallow-assurance:bugfix\` with this issue's URL.\n"
    fi
  } >"$out"
}

file_crasher() {
  local pkg=$1 target=$2 hash=$3 file=$4
  local title="Nightly fuzz crasher: ${pkg} ${target}"
  local list match number url body="$work/body"

  if ((label_ensured == 0)); then
    if gh label create "$label" --force; then
      label_ensured=1
    else
      echo "fuzz-nightly: could not ensure label $label" >&2
      status=1
    fi
  fi

  if ! list=$(gh issue list --label "$label" --state open --json number,title,url --limit 200); then
    echo "fuzz-nightly: could not list $label issues; not filing $title" >&2
    status=1
    return
  fi
  if ! match=$(jq -r --arg t "$title" '[.[] | select(.title == $t)][0] // empty | "\(.number)\t\(.url)"' <<<"$list"); then
    echo "fuzz-nightly: could not parse $label issues; not filing $title" >&2
    status=1
    return
  fi

  if [[ -n $match ]]; then
    number=${match%%$'\t'*}
    url=${match#*$'\t'}
    write_body "$body" "$pkg" "$target" "$hash" "$file" "$url"
    if ! gh issue comment "$number" --body-file "$body"; then
      echo "fuzz-nightly: could not comment on #$number for $title" >&2
      status=1
    fi
  else
    write_body "$body" "$pkg" "$target" "$hash" "$file" ""
    if ! gh issue create --title "$title" --label "$label" --body-file "$body"; then
      echo "fuzz-nightly: could not open an issue for $title" >&2
      status=1
    fi
  fi
}

while IFS= read -r importpath; do
  [[ -z $importpath ]] && continue
  if [[ $importpath == "$module" ]]; then
    pkg=.
  else
    pkg=${importpath#"$module"/}
  fi

  if ! list=$(go test -list '^Fuzz' "./$pkg"); then
    echo "fuzz-nightly: could not list fuzz targets in $pkg" >&2
    status=1
    continue
  fi

  while IFS= read -r target; do
    [[ $target =~ ^Fuzz ]] || continue
    corpus_dir="$pkg/testdata/fuzz/$target"
    snapshot "$corpus_dir" "$work/before"

    echo "::group::$pkg $target"
    failed=0
    go test -run '^$' -fuzz "^${target}\$" -fuzztime "$fuzztime" "./$pkg" || failed=1
    echo "::endgroup::"

    if ((failed == 0)); then continue; fi
    status=1

    snapshot "$corpus_dir" "$work/after"
    new=$(LC_ALL=C comm -13 "$work/before" "$work/after")
    if [[ -z $new ]]; then
      echo "fuzz-nightly: $pkg $target failed without writing a new input; no issue filed" >&2
      continue
    fi
    while IFS= read -r hash; do
      file_crasher "$pkg" "$target" "$hash" "$corpus_dir/$hash"
    done <<<"$new"
  done <<<"$list"
done <<<"$packages"

exit "$status"

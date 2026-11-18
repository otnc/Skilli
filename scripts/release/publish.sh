#!/usr/bin/env bash
set -euo pipefail

version=${TAG#v}
assets=(
  "dist/Skilli-Setup-$version-x64.exe"
  "dist/windows-amd64/skilli-$version-windows-amd64.tar.gz"
  "dist/windows-amd64/update-windows-amd64.json"
)
for asset in "${assets[@]}"; do
  [[ -s $asset ]] || { printf '配布ファイルがありません: %s\n' "$asset" >&2; exit 1; }
done

if [[ $RETRY == false ]]; then
  git tag "$TAG"
  git push origin "refs/tags/$TAG"
fi

flags=(--latest=true)
if [[ $version == *-* ]]; then
  flags=(--prerelease --latest=false)
fi
if draft=$(gh release view "$TAG" --json isDraft --jq '.isDraft' 2>/dev/null); then
  if [[ $draft == false ]]; then
    names=$(gh release view "$TAG" --json assets --jq '.assets[].name')
    for asset in "${assets[@]}"; do
      grep -Fxq "${asset##*/}" <<< "$names" || {
        printf '公開済みリリースに %s がありません。\n' "${asset##*/}" >&2
        exit 1
      }
    done
    exit 0
  fi
  gh release upload "$TAG" "${assets[@]}" --clobber
else
  gh release create "$TAG" --verify-tag --draft --generate-notes "${assets[@]}"
fi
gh release edit "$TAG" --draft=false "${flags[@]}"

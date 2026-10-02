#!/usr/bin/env bash
# Usage: ./scripts/release.sh v0.10.0
# Push an annotated tag at main's current commit to start the release workflow.
set -euo pipefail

die() {
    printf 'Error: %s\n' "$*" >&2
    exit 1
}

[[ $# -eq 1 ]] || die "Usage: $0 vMAJOR.MINOR.PATCH"
version=$1
[[ $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "Version must have the form vMAJOR.MINOR.PATCH"

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
cd -- "$script_dir/.."

[[ $(git branch --show-current) == main ]] || die "Check out main before releasing"
[[ -z $(git status --porcelain) ]] || die "Working tree must be clean before releasing"

commit=$(git rev-parse HEAD)
remote_refs=$(git ls-remote --exit-code origin refs/heads/main)
remote_commit=${remote_refs%%$'\t'*}
[[ $commit == "$remote_commit" ]] || die "Local main differs from origin/main; sync it before releasing"

# Every screen and every account, which a pull request's run leaves out.
make e2e-full || die "Browser tests failed; fix them before releasing"

if git show-ref --verify --quiet "refs/tags/$version"; then
    die "Local tag $version already exists"
fi
remote_tag=$(git ls-remote origin "refs/tags/$version")
[[ -z $remote_tag ]] || die "Remote tag $version already exists"

git tag -a "$version" "$commit" -m "Release $version"
if ! git push origin "refs/tags/$version"; then
    die "Tag push failed. Local tag $version remains; inspect the remote before retrying"
fi

printf 'Pushed %s at %s. The release workflow is triggered by this tag.\n' "$version" "$commit"
printf 'Check its status with: gh run list --workflow release.yaml --branch %s\n' "$version"

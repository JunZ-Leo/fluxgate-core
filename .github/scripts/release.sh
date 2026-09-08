#!/usr/bin/env bash
set -euo pipefail

fail() {
  printf 'Release validation failed: %s\n' "$*" >&2
  exit 1
}

validate_version() {
  local identifier
  local identifiers=()
  [[ "$VERSION" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]] ||
    fail "expected vMAJOR.MINOR.PATCH with an optional SemVer prerelease"
  if [[ "$VERSION" == *-* ]]; then
    IFS=. read -r -a identifiers <<< "${VERSION#*-}"
    for identifier in "${identifiers[@]}"; do
      if [[ "$identifier" =~ ^[0-9]+$ && "$identifier" == 0?* ]]; then
        fail "numeric prerelease identifiers must not have leading zeroes"
      fi
    done
  fi
}

resolve_commit() {
  [[ "$1" =~ ^([0-9a-f]{40}|[0-9a-f]{64})$ ]] || fail "expected a full build object SHA"
  BUILD_SHA=$(git rev-parse --verify "$1^{commit}") || fail "build object is not a commit"
  [[ "$(git rev-parse --verify HEAD)" == "$BUILD_SHA" ]] ||
    fail "checkout does not match the captured build commit"
}

read_remote_tag() {
  local refs status object ref peeled=""
  REMOTE_TAG_SHA=""
  if refs=$(git ls-remote --exit-code origin "refs/tags/$VERSION" "refs/tags/$VERSION^{}"); then
    while read -r object ref; do
      case "$ref" in
        "refs/tags/$VERSION") REMOTE_TAG_SHA="$object" ;;
        "refs/tags/$VERSION^{}") peeled="$object" ;;
      esac
    done <<< "$refs"
    REMOTE_TAG_SHA="${peeled:-$REMOTE_TAG_SHA}"
    [[ -n "$REMOTE_TAG_SHA" ]] || fail "remote returned no matching tag"
  else
    status=$?
    [[ "$status" == 2 ]] || fail "could not inspect the release tag on origin"
  fi
  [[ -z "$REMOTE_TAG_SHA" || "$REMOTE_TAG_SHA" == "$BUILD_SHA" ]] ||
    fail "existing tag $VERSION points to a different commit"
}

case "${1:-}" in
  metadata)
    resolve_commit "${GITHUB_SHA:?}"
    release=false
    prerelease=false
    case "${GITHUB_EVENT_NAME:?}" in
      workflow_dispatch)
        VERSION="${INPUT_VERSION:-}"
        release=true
        ;;
      push)
        [[ "${GITHUB_REF:?}" == refs/tags/v* ]] || fail "only version tag pushes can release"
        VERSION="${GITHUB_REF#refs/tags/}"
        release=true
        ;;
      pull_request)
        VERSION="v0.0.0-dev.$BUILD_SHA"
        ;;
      *) fail "unsupported workflow event" ;;
    esac
    if [[ "$release" == true ]]; then
      validate_version
      read_remote_tag
      if [[ "$GITHUB_EVENT_NAME" == push && -z "$REMOTE_TAG_SHA" ]]; then
        fail "pushed release tag no longer exists"
      fi
      [[ "$VERSION" != *-* ]] || prerelease=true
    fi
    {
      printf 'sha=%s\n' "$BUILD_SHA"
      printf 'version=%s\n' "$VERSION"
      printf 'package-version=%s\n' "${VERSION#v}"
      printf 'release=%s\n' "$release"
      printf 'prerelease=%s\n' "$prerelease"
    } >> "${GITHUB_OUTPUT:?}"
    ;;
  ensure-tag)
    VERSION="${RELEASE_VERSION:?}"
    validate_version
    resolve_commit "${RELEASE_SHA:?}"
    case "${GITHUB_EVENT_NAME:?}" in
      workflow_dispatch|push) ;;
      *) fail "this event cannot publish" ;;
    esac
    read_remote_tag
    if [[ -z "$REMOTE_TAG_SHA" ]]; then
      [[ "$GITHUB_EVENT_NAME" == workflow_dispatch ]] || fail "pushed release tag no longer exists"
      # Push the captured commit directly; never follow a branch or replace a tag.
      if ! git push origin "$BUILD_SHA:refs/tags/$VERSION"; then
        printf 'Tag push failed; checking whether another run created the same tag.\n' >&2
      fi
      read_remote_tag
      [[ "$REMOTE_TAG_SHA" == "$BUILD_SHA" ]] || fail "release tag was not created"
    fi
    ;;
  *) fail "expected metadata or ensure-tag" ;;
esac

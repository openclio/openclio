#!/usr/bin/env sh
set -eu

OUT_FILE="${1:-THIRD_PARTY_NOTICES.txt}"
TMP_FILE="$(mktemp)"

cleanup() {
  rm -f "${TMP_FILE}"
}
trap cleanup EXIT

go list -m -f '{{if and .Path .Version}}{{.Path}} {{.Version}}{{end}}' all \
  | sort \
  > "${TMP_FILE}"

{
  echo "Third-Party Notices"
  echo "==================="
  echo
  echo "This file lists direct and transitive Go modules used to build openclio."
  echo "License terms for each dependency are governed by the respective project."
  echo
  echo "Generated: $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  echo
  echo "MODULE VERSION"
  echo "--------------"
  cat "${TMP_FILE}"
} > "${OUT_FILE}"

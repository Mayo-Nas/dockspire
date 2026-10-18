#!/bin/bash
# First-install entry point for a verified, extracted Linux release.
set -euo pipefail

directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$directory"
mode=${1:-}
if [ "$#" -gt 1 ]; then printf '%s\n' 'Usage: bash install.sh [--check-only|--init-admin-stdin]'; exit 2; fi
case "$mode" in ''|--check-only|--init-admin-stdin) ;; *) printf '%s\n' 'Usage: bash install.sh [--check-only|--init-admin-stdin]'; exit 2 ;; esac
if [ ! -f ./SHA256SUMS ] || [ ! -f ./stacklane ]; then
  printf '%s\n' 'Run this installer from a verified, extracted Dockspire Linux release, not the source checkout.'
  exit 1
fi
sha256sum -c SHA256SUMS
sh ./install-preview.sh --check-only
if [ "$mode" = --check-only ]; then exit 0; fi
if [ ! -f ./data/admin.json ]; then
  if [ "$mode" = --init-admin-stdin ]; then
    ./stacklane --data-dir ./data --init-admin
  else
    if [ ! -t 0 ]; then
      printf '%s\n' 'First installation needs an interactive password prompt, or pipe a password with --init-admin-stdin.'
      exit 1
    fi
    IFS= read -r -s -p 'Choose the admin password (at least 15 characters): ' task_password
    printf '\n'
    IFS= read -r -s -p 'Confirm the admin password: ' task_confirmation
    printf '\n'
    if [ "$task_password" != "$task_confirmation" ]; then
      unset task_password task_confirmation
      printf '%s\n' 'Passwords do not match. Nothing was started.'
      exit 1
    fi
    printf '%s\n' "$task_password" | ./stacklane --data-dir ./data --init-admin
    unset task_password task_confirmation
  fi
elif [ "$mode" = --init-admin-stdin ]; then
  printf '%s\n' 'An administrator already exists. This installer never replaces or resets it.'
  exit 1
fi
sh ./install-preview.sh

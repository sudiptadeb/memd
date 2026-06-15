#!/usr/bin/env bash
#
# setup-browser.sh — install Playwright + a headless Chromium for browser-based
# QA of the memd web UI (screenshots, end-to-end checks, responsive verification).
#
# Run once on a fresh environment:
#
#     bash build/setup-browser.sh
#
# To make browser access available automatically in every Claude Code web
# session, set this script as the environment's *setup script* (see
# https://code.claude.com/docs/en/claude-code-on-the-web), or call it from a
# SessionStart hook.
#
# After it runs, a Node script can drive Chromium by importing Playwright when
# executed with its working directory set to $BROWSER_TOOLS_DIR (default
# ~/.browser-tools), e.g.:
#
#     cd ~/.browser-tools && node /path/to/script.mjs   # import { chromium } from "playwright"
#
set -euo pipefail

DIR="${BROWSER_TOOLS_DIR:-$HOME/.browser-tools}"
PW_VERSION="${PLAYWRIGHT_VERSION:-latest}"

command -v node >/dev/null 2>&1 || { echo "node is required but not found on PATH" >&2; exit 1; }

echo "→ Installing Playwright (${PW_VERSION}) into ${DIR}"
mkdir -p "$DIR"
cd "$DIR"
[ -f package.json ] || npm init -y >/dev/null
npm install --no-fund --no-audit "playwright@${PW_VERSION}"

echo "→ Installing the Chromium browser binary"
# --with-deps also installs the required OS libraries, but that needs root (apt).
# Fall back to a plain browser download when system deps can't be installed.
if [ "$(id -u)" = "0" ] || command -v sudo >/dev/null 2>&1; then
  npx playwright install --with-deps chromium || npx playwright install chromium
else
  npx playwright install chromium
fi

echo "✓ Playwright + Chromium ready in ${DIR}"
echo "  Drive it from a Node script run with cwd=${DIR}:  import { chromium } from 'playwright'"

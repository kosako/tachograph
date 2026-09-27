#!/bin/bash
# <xbar.title>tachograph</xbar.title>
# <xbar.desc>Rate-limit / context gauges for Claude Code and Codex CLI.</xbar.desc>
# <xbar.dependencies>tacho</xbar.dependencies>
# <xbar.abouturl>https://github.com/kosako/tachograph</xbar.abouturl>
# SwiftBar may not see your shell's PATH, and the line below only adds
# /opt/homebrew/bin and ~/go/bin. If tacho lives elsewhere (e.g. GOBIN, or an
# npm global under nvm / mise), install the plugin with
# `tacho setup swiftbar --write` instead, which writes tacho's absolute path
# on the `exec` line.
export PATH="/opt/homebrew/bin:$HOME/go/bin:$PATH"
# Optional display tweaks (see the README's SwiftBar section):
# export TACHO_APPEARANCE=light   # near-black logo/track for a light menu bar
# export TACHO_SWIFTBAR_TEXT=1    # moon-dial text instead of the gauge image
exec tacho swiftbar

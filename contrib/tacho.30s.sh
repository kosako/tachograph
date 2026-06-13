#!/bin/bash
# <xbar.title>tachograph</xbar.title>
# <xbar.desc>Rate-limit / context gauges for Claude Code and Codex CLI.</xbar.desc>
# <xbar.dependencies>tacho</xbar.dependencies>
# <xbar.abouturl>https://github.com/kosako/tachograph</xbar.abouturl>
export PATH="/opt/homebrew/bin:$HOME/go/bin:$PATH"
exec tacho swiftbar

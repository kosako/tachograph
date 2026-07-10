# 統一JSONスキーマ v0.2

`tacho status --json` が出力する、コレクタ層とレンダラ層の境界となるスキーマ。
本ファイルが仕様の正本。Goの型定義は `internal/schema/schema.go`。

## 設計方針

- **キー集合は常に一定**: 値が取れないフィールドは省略せず `null` を出す。
  レンダラ側の分岐を「null チェック」に統一するため
- **縮退はスキーマで表現する**: レートリミットという概念が存在しないバックエンド
  (Bedrock等)では `limits: null` とし、代わりに `fallback` を主表示とする
- **時刻はすべて ISO 8601(RFC 3339)文字列**: Codexが返すepoch秒は変換して載せる

## トップレベル

```jsonc
{
  "schema_version": "0.2",
  "generated_at": "2026-06-12T21:00:00+09:00",  // この JSON を生成した時刻
  "tools": [ /* ツールごとのエントリ。検出されないツールも available:false で常に載る */ ]
}
```

## ツールエントリ

```jsonc
{
  "tool": "codex",                    // "claude-code" | "codex"
  "available": true,                  // データソース(セッションファイル等)が見つかったか
  "error": null,                      // 取得失敗時 {"code": "...", "message": "..."}
  "stale": false,                     // 最終観測データが古いとき true(閾値はツール別: Claude transcript経路=60分 StaleAfterMinutes、Codex=5時間)
  "collected_at": "2026-05-24T22:40:28+09:00",  // データの実観測時刻(イベントのtimestamp)
  "backend": "subscription",          // "subscription" | "api" | "bedrock" | "vertex" | "unknown"
  "plan": "prolite",                  // プラン名。不明なら null
  "model": {
    "id": "gpt-5.4-codex",
    "display_name": null,             // 表示名が別途取れる場合のみ
    "effort": null                    // reasoning effort(low|medium|high|xhigh|max)。Claude statusline経由のみ、非対応/不明は null
  },
  "session": {
    "id": "019e5933-...",
    "cwd": "/Users/example/dev/...",
    "context_window": 258400,
    "context_used_pct": 38.2,         // 算出不能なら null
    "tokens": {
      "input": 986913,
      "cached_input": 803712,
      "output": 2207,
      "total": 989120
    },
    "transcript_path": "/Users/example/.../<session>.jsonl"  // 任意。session_today算出に使用
  },
  "limits": [                         // レートリミット枠。概念が存在しない場合は null
    {
      "window": "5h",                 // "5h" | "weekly"(将来追加あり得る)
      "window_minutes": 300,
      "used_pct": 5.0,                // 使用率(残量ではない)
      "resets_at": "2026-06-13T02:00:58+09:00",
      "saved_resets": null            // Codex「保存式リセット」用に予約。MVPでは未使用
    }
  ],
  "credits": null,                    // クレジット残高。概念がない/不明なら null
  "fallback": {                       // limits が null のときレンダラが主表示に使う
    "session_tokens": 989120,
    "estimated_cost_usd": null
  },
  "daily": {                          // 当日(ローカル日付)の全セッション合計。ログの走査に失敗したとき(集計値が不明のとき)は null(0 と区別)
    "tokens": 12704565,               // 新規トークン(cache_read等の再読込は除外)
    "cost_usd": null                  // 料金表ベースの推定コスト(未確定時null)
  },
  "session_today": {                  // 現セッションの当日分のみ。Claudeのみ(Codexは累積記録のためnull)
    "tokens": 68000,
    "cost_usd": 1.84
  }
}
```

## フィールド規約

| フィールド | 規約 |
|---|---|
| `available` | データソース自体の有無。`false` のとき他フィールドはすべて null |
| `error` | 取得を試みて失敗したときのみ非null。`available:false`(未インストール等)はエラーではない |
| `stale` | `collected_at` が古いとき true。閾値はツール別: Claude(transcript経路)=60分(`StaleAfterMinutes`)、Codex=5時間(ライブ入力が無くリミット枠が数時間有効なため)。レンダラは灰色表示などに使う |
| `backend` | 必須。リミット概念の有無の判定に使う(`bedrock`/`vertex`/`api` → `limits: null`) |
| `session.transcript_path` | 例外的に nil 時はキーごと省略(`omitempty`)。「キー集合は常に一定」原則の唯一の例外 |
| `limits` | nullable。配列のときは `window_minutes` 昇順 |
| `used_pct` | 0–100。「使った割合」。残量表示はレンダラ側で `100 - used_pct` |
| `fallback` | `limits: null` のときの主表示(セッショントークン数+推定コスト) |
| `daily.cost_usd` / `session_today.cost_usd` | 料金表の `cache_read` / `cache_write` を使う推定値。Claude transcript が `cache_creation.ephemeral_1h_input_tokens` を持つ場合、1h cache write は input 単価の2倍として計算 |

## データソース対応表

| フィールド | claude-code | codex |
|---|---|---|
| `model` | statusline stdin JSON / transcripts の `message.model` | sessions JSONL `turn_context.payload.model` |
| `model.effort` | statusline `effort.level`(ライブ値、`/effort` 変更も反映。transcripts経路や非対応モデルでは null) | —(null) |
| `session.tokens` | statusline `context_window.total_*` / transcripts の `message.usage` 集計 | `token_count.payload.info.total_token_usage` |
| `session.context_window` | statusline `context_window.context_window_size`(transcripts経路では null) | `token_count.payload.info.model_context_window` |
| `limits` | statusline `rate_limits.five_hour/seven_day`(transcripts経路では null) | `token_count.payload.rate_limits.primary/secondary` |
| `plan` | —(null、statusline JSONに含まれない) | `rate_limits.plan_type` |
| `credits` | —(null) | `rate_limits.credits` |

Claude Code のトークン集計規約: `input` は `input_tokens + cache_creation + cache_read`
の総和(Codexの「inputはcached含む」と意味を揃える)。`cached_input` は `cache_read` の総和。
`daily` は `projects` 配下の通常セッションに加え、同セッション配下の subagents / workflows
transcript も集計する。ログディレクトリの走査自体に失敗したとき(データ未生成の
「実在する 0」と区別できないとき)は `daily` を null にする(不明は null 原則)。`session_today` も現セッション transcript と、その同名セッション
ディレクトリ配下の subagents / workflows transcript を集計する。

## バージョニング

- 後方互換の追加(フィールド追加)はマイナー更新: `0.1` → `0.2`
- 既存フィールドの意味変更・削除はメジャー更新とし、`schema_version` で判定可能にする

### 変更履歴

- `0.2`: v0.1 以降に追加されたフィールドを版に反映(後方互換の追加のみ):
  `tool.daily`、`tool.session_today`、`model.effort`、`tool.credits`
- `0.1`: 初版

# 統一JSONスキーマ v0.1

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
  "schema_version": "0.1",
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
  "stale": false,                     // 最終観測データが古い(15分超)とき true
  "collected_at": "2026-05-24T22:40:28+09:00",  // データの実観測時刻(イベントのtimestamp)
  "backend": "subscription",          // "subscription" | "api" | "bedrock" | "vertex" | "unknown"
  "plan": "prolite",                  // プラン名。不明なら null
  "model": {
    "id": "gpt-5.4-codex",
    "display_name": null              // 表示名が別途取れる場合のみ
  },
  "session": {
    "id": "019e5933-...",
    "cwd": "/Users/kosako/dev/...",
    "context_window": 258400,
    "context_used_pct": 38.2,         // 算出不能なら null
    "tokens": {
      "input": 986913,
      "cached_input": 803712,
      "output": 2207,
      "total": 989120
    }
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
  }
}
```

## フィールド規約

| フィールド | 規約 |
|---|---|
| `available` | データソース自体の有無。`false` のとき他フィールドはすべて null |
| `error` | 取得を試みて失敗したときのみ非null。`available:false`(未インストール等)はエラーではない |
| `stale` | `collected_at` が現在時刻から15分超過。レンダラは灰色表示などに使う |
| `backend` | 必須。リミット概念の有無の判定に使う(`bedrock`/`vertex`/`api` → `limits: null`) |
| `limits` | nullable。配列のときは `window_minutes` 昇順 |
| `used_pct` | 0–100。「使った割合」。残量表示はレンダラ側で `100 - used_pct` |
| `fallback` | `limits: null` のときの主表示(セッショントークン数+推定コスト) |

## データソース対応表

| フィールド | claude-code | codex |
|---|---|---|
| `model` | statusline stdin JSON / transcripts の `message.model` | sessions JSONL `turn_context.payload.model` |
| `session.tokens` | statusline `context_window.total_*` / transcripts の `message.usage` 集計 | `token_count.payload.info.total_token_usage` |
| `session.context_window` | statusline `context_window.context_window_size`(transcripts経路では null) | `token_count.payload.info.model_context_window` |
| `limits` | statusline `rate_limits.five_hour/seven_day`(transcripts経路では null) | `token_count.payload.rate_limits.primary/secondary` |
| `plan` | —(null、statusline JSONに含まれない) | `rate_limits.plan_type` |
| `credits` | —(null) | `rate_limits.credits` |

Claude Code のトークン集計規約: `input` は `input_tokens + cache_creation + cache_read`
の総和(Codexの「inputはcached含む」と意味を揃える)。`cached_input` は `cache_read` の総和。

## バージョニング

- 後方互換の追加(フィールド追加)はマイナー更新: `0.1` → `0.2`
- 既存フィールドの意味変更・削除はメジャー更新とし、`schema_version` で判定可能にする

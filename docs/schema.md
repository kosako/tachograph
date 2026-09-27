# 統一JSONスキーマ v3.0

`tacho status --json` が出力する、コレクタ層とレンダラ層の境界となるスキーマ。
本ファイルが仕様の正本。Goの型定義は `internal/schema/schema.go`。

## 設計方針

- **キー集合は常に一定**: 値が取れないフィールドは省略せず `null` を出す。
  レンダラ側の分岐を「null チェック」に統一するため
- **縮退はスキーマで表現する**: レートリミットという概念が存在しないバックエンド
  (Bedrock等)では `limits: null` とし、代わりに `fallback` を主表示とする
- **時刻はすべて ISO 8601(RFC 3339)文字列**: Claude statusline / Codex が返す epoch 秒
  (`resets_at`)は変換して載せる。オフセットはローカルタイムゾーン

## トップレベル

```jsonc
{
  "schema_version": "3.0",
  "generated_at": "2026-06-12T21:00:00+09:00",  // この JSON を生成した時刻(stale もこの時刻で判定)。--no-cache なしでは最大 30 秒前のキャッシュを返すことがある
  "tools": [ /* ツールごとのエントリ。検出されないツールも available:false で常に載る */ ]
}
```

## ツールエントリ

以下の例は全キーを示すため、codex のエントリに Claude のみのフィールド(`session.transcript_path` / `session_today`)の値も入れている。
実際の codex エントリでは `session.transcript_path` はキーごと省略され、`session_today` は null になる。

```jsonc
{
  "tool": "codex",                    // "claude-code" | "codex"
  "available": true,                  // データソース(セッションファイル等)が見つかったか
  "error": null,                      // 取得失敗時 {"code": "...", "message": "..."}
  "stale": false,                     // 最終観測データが古いとき true(閾値はツール別: Claude transcript / snapshot 経路=60分 StaleAfterMinutes、Codex=5時間)
  "collected_at": "2026-05-24T22:40:28+09:00",  // データの実観測時刻(Claude transcript経路は最後の usage 行、Codex は最後の利用可能な token_count イベント(制限到達時などに書かれる空の token_count は読み飛ばす)の timestamp で、リミットを別の token_count から取ったときは古い方。statusline 経由は受信時刻)。不明なら null
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
    "tokens": {                       // セッション累計。算出不能なら null
      "input": 986913,
      "cached_input": 803712,
      "output": 2207,
      "total": 989120                 // input + output(input は cached_input を含む。Codex は total_token_usage.total_tokens をそのまま載せる)
    },
    "transcript_path": "/Users/example/.../<session>.jsonl"  // 任意。session_today算出に使用
  },
  "limits": [                         // レートリミット枠。概念が存在しない場合は null
    {
      "window": "5h",                 // "5h" | "weekly"。Codex がそれ以外の長さの枠を返すと "<N>h"(60分の倍数のとき)/ "<N>m"
      "window_minutes": 300,
      "used_pct": 5.0,                // 使用率(残量ではない)
      "resets_at": "2026-06-13T02:00:58+09:00",  // 不明なら null
      "saved_resets": null            // Codex「保存式リセット」用に予約。現状は常に null
    }
  ],
  "credits": null,                    // クレジット残高。概念がない/不明なら null
  "fallback": {                       // limits が null のときレンダラが主表示に使う
    "session_tokens": 989120,
    "estimated_cost_usd": null
  },
  "daily": {                          // 当日(ローカル日付)の全セッション合計。ログの走査に失敗したとき(集計値が不明のとき)は null(0 と区別)
    "tokens": 12704565,               // 課金対象トークン = input + output(session.tokens.total と同じ尺度。2.0 で意味変更 #234)
    "input": 12504028,                // cache write / cache read を含む
    "cached_input": 11800000,         // input のうち cache read
    "output": 200537,
    "cost_usd": null                  // 料金表ベースの推定コスト。推定額が 0 のとき(料金表にあるモデルの使用が無いときなど)は null
  },
  "session_today": {                  // 現セッションの当日分のみ。Claudeのみ(Codexは累積記録のためnull)。内訳は daily と同じ
    "tokens": 68000,
    "input": 66000,
    "cached_input": 60000,
    "output": 2000,
    "cost_usd": 1.84
  }
}
```

## フィールド規約

| フィールド | 規約 |
|---|---|
| `available` | データソース自体の有無。`false` のときデータ由来の nullable フィールドはすべて null(`tool` / `available` / `stale` などの必須フィールドは除く) |
| `error` | 取得を試みて失敗したときのみ非null。`available:false`(未インストール等)はエラーではない。非null のときは `available: true`・`backend: "unknown"` で、他のデータ由来フィールド(`daily` / `session_today` を含む)はすべて null。`code` は現状 `home_dir` / `read_error`(両ツール)、`no_usage`(Claude)、`no_token_count`(Codex) |
| `stale` | `collected_at` が古いとき true。閾値はツール別: Claude(transcript経路・snapshot経路とも)=60分(`StaleAfterMinutes`)、Codex=5時間(ライブ入力が無くリミット枠が数時間有効なため)。レンダラは灰色表示などに使う。statusline 以外の経路で Claude が snapshot から出るとき、`session` / `fallback` / `session_today` は「直近に観測したセッション」の値で、stale になると null(不明)に落ちる。`limits` / `model` / `plan` / `credits` は最大 30 日保持(#235) |
| `backend` | 必須。リミット概念の有無の判定に使う(`bedrock`/`vertex`/`api` → `limits: null`) |
| `session.transcript_path` | 例外的に nil 時はキーごと省略(`omitempty`)。「キー集合は常に一定」原則の唯一の例外 |
| `limits` | nullable。並び順はツールの報告順(Claude は 5h → weekly、Codex は `rate_limits.primary` → `secondary`)で、`window_minutes` 昇順は保証しない(#268)。枠は配列の位置ではなく `window` / `window_minutes` で引く |
| `used_pct` | 「使った割合」(%)。ツールが報告した値をそのまま入れる(通常は 0–100 だが範囲外の補正はせず、0–100 に丸めるのは表示のときだけ)。JSON はこの意味のまま。レンダラは既定で残量 `100 - used_pct` を表示し(`limits.display: used` で使用率)、色分けは `used_pct` 基準(#223 / #228) |
| `fallback` | `limits: null` のときの主表示(セッショントークン数+推定コスト)。値自体は `limits` の有無に関わらず取れる限り入る(`session_tokens` は `session.tokens.total` と同じ値) |
| `daily.tokens` / `session_today.tokens` | 課金対象トークン(`input` + `output`。`input` は cache write / cache read 込み)で、`session.tokens.total` および `cost_usd` と分母が同じ(#234)。`input` / `cached_input` / `output` の内訳を併せて持つ。Codex は `total_token_usage` の増分をそのまま使う |
| `daily.cost_usd` / `session_today.cost_usd` | 料金表の `cache_read` / `cache_write` を使う推定値。Claude transcript が `cache_creation.ephemeral_1h_input_tokens` を持つ場合、1h cache write は input 単価の2倍として計算。Codex の daily は `token_count` イベント単位の増分をその時点の `turn_context.model` 単価で積算(セッション内のモデル切替に追随)。Codex の `fallback.estimated_cost_usd`(session cost)は累積値しか持たないため「現在モデル × 全累積」の概算 |

## データソース対応表

| フィールド | claude-code | codex |
|---|---|---|
| `model` | statusline stdin JSON / transcripts の `message.model` | sessions JSONL `turn_context.payload.model` |
| `model.effort` | statusline `effort.level`(ライブ値、`/effort` 変更も反映。transcripts経路や非対応モデルでは null) | —(null) |
| `session.tokens` | 現セッションの transcript ツリー(本体 `<session>.jsonl` と、同名ディレクトリ配下の subagents / workflows transcript)の `message.usage` 集計。`daily` / `session_today`、Claude Code が渡す cost と同じ範囲(3.0 から。2.0 までは本体 1 ファイルだけ、#262)。statusline 経路も `transcript_path` から集計。v2.1.132 以降の statusline `context_window.total_*` は現在コンテキスト量でありセッション累計ではないため使わない。本体の transcript が読めない / usage が無いとき、または配下の transcript のどれかが読めないときは null(一部だけの合計は出さない)) | `token_count.payload.info.total_token_usage` |
| `session.context_window` | statusline `context_window.context_window_size`(transcripts経路では null) | `token_count.payload.info.model_context_window` |
| `session.context_used_pct` | statusline `context_window.used_percentage`(transcripts経路では null) | `last_token_usage.total_tokens` ÷ `model_context_window` × 100(直近リクエストの総量による近似) |
| `limits` | statusline `rate_limits.five_hour/seven_day`(transcripts経路では null) | `token_count.payload.rate_limits.primary/secondary`。`rate_limits.limit_id` が `codex`(または無し)の token_count だけから取る(`premium` やモデル別の枠の token_count はアカウントの枠ではない)。`plan` / `credits` / `backend` も同じ token_count から |
| `plan` | —(null、statusline JSONに含まれない) | `rate_limits.plan_type` |
| `backend` | 環境変数から判定: `CLAUDE_CODE_USE_BEDROCK` → `bedrock`、`CLAUDE_CODE_USE_VERTEX` → `vertex`、`ANTHROPIC_API_KEY` → `api`、いずれも無ければ `subscription`(上から優先。statusline 経路では `api` と判定してもレートリミット枠があれば `subscription`。transcripts経路では tacho を実行したプロセスの環境変数を見る) | `rate_limits.plan_type` があれば `subscription`、無ければ `unknown` |
| `credits` | —(null) | `rate_limits.credits` |
| `fallback.estimated_cost_usd` | statusline `cost.total_cost_usd`(Claude Code 自身が算出した値で、tacho の料金表は使わない。transcripts経路では null) | 料金表 × `total_token_usage`(現在モデル × 全累積の概算) |

Claude Code のトークン集計規約: `input` は `input_tokens + cache_creation + cache_read`
の総和(Codexの「inputはcached含む」と意味を揃える)。`cached_input` は `cache_read` の総和。
この規約は `session.tokens` / `session_today` / `daily` で共通(2.0。集計範囲がそろったのは 3.0)。
いずれも同一レスポンスは `message.id` + `requestId` で重複を除いて1回だけ数える(content block ごとに usage を繰り返す行や、
resume / compaction でコピーされた行を二重計上しない)。
`daily` は `projects` 配下の通常セッションに加え、同セッション配下の subagents / workflows
transcript も集計する。ログディレクトリの走査自体に失敗したとき(データ未生成の
「実在する 0」と区別できないとき)は `daily` を null にする(不明は null 原則)。`session_today` も現セッション transcript と、その同名セッション
ディレクトリ配下の subagents / workflows transcript を集計する(Claude の `session.tokens` も同じツリーを数え、`session_today` はその当日分)。ただし `session_today` は `daily` と違い、
不明と 0 を区別しない(読めない transcript は 0 として扱う)。`session.transcript_path` が無いとき
(Codex、stale な snapshot など)は null。

## バージョニング

- 後方互換の追加(フィールド追加)はマイナー更新: `0.1` → `0.2`
- 既存フィールドの意味変更・削除はメジャー更新とし、`schema_version` で判定可能にする

### 変更履歴

基準は初版 commit の v0.1(`credits` は初版から存在)。

- `3.0`: Claude の `session.tokens` / `fallback.session_tokens` の意味変更(現セッションの transcript 本体 1 ファイル → 同名ディレクトリ配下の subagents / workflows transcript を含むツリー全体。`session_today` / `daily`、Claude Code が渡す cost と集計範囲をそろえ、「当日分 > 累計」が起きないようにした、#262)
- `2.0`: `daily.tokens` / `session_today.tokens` の意味変更(cache read を除いた「新規トークン」→ cache read を含む課金対象トークン、#234)。追加(後方互換): `daily.input` / `daily.cached_input` / `daily.output`(`session_today` も同じ)。意味変更: statusline 以外の経路で Claude が stale な snapshot から出るとき、`session` / `fallback` / `session_today` を null にする(従来は直近に観測した値のまま、#235)
- `1.0`: v0.1 のまま出荷されてきた追加と意味変更を版に反映
  - 追加(後方互換): `tool.daily`、`tool.session_today`、`session.transcript_path`、`model.effort`
  - 意味変更(メジャー要因): `stale` の閾値(15分 → Claude 60分 / Codex 5時間)、
    `daily` の走査失敗時の扱い(0 → null、#180)
- `0.1`: 初版

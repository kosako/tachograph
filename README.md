# tachograph

日本語 | [English](README.en.md)

> コーディングエージェントのためのコンパクトな計器盤。

`tacho` は、AIコーディングエージェントの稼働状況と残り枠を「パッと見」で把握するためのCLIです:

- セッションごとの現在使用モデル
- レートリミット使用率(5時間枠 / 週次枠)とリセット時刻
- コンテキストウィンドウ使用率

対応エージェント: **Claude Code** / **Codex CLI**

## なぜ "tachograph"?

タコグラフは、トラックに搭載が義務付けられた、運転時間・休憩義務・運転再開可能時刻を記録する計器です。このツールがコーディングエージェントに対してやることはまさにそれです — レートリミット枠をどれだけ使ったか、いつリセットされるか、いま何が動いているかを記録・表示します。

## 設計原則

1. **計器であって観測基盤ではない。** ログ蓄積・コスト分析・ダッシュボードは作らない。「パッと見」に特化する
2. **コレクタ(収集)とレンダラ(表示)の分離。** コアは統一スキーマのJSON(`tacho status --json`)を出力し、表示先はプラガブル
3. **常駐デーモンなし。** オンデマンド収集+短命のファイルキャッシュのみ
4. **薄く作る。** エージェントが既にディスクへ書いているデータを読むだけ

## インストール

```sh
go install github.com/kosako/tachograph/cmd/tacho@latest
```

## 使い方

```sh
tacho                  # ワンショット表示(1エージェント=1行)
tacho watch -n 5       # 定期再描画
tacho status --json    # 統一スキーマJSON(docs/schema.md 参照)
tacho statusline       # Claude Code statusLineアダプタ(stdinのJSONを読む)
tacho cmux push|clear  # cmuxサイドバーのピルを手動操作
```

```
claude Fable 5              ctx 32%  5h ███░░░░░ 37% ↻10:30  wk █████░░░ 68% ↻17:00
codex  gpt-5.5        ⚠6h   ctx 13%  5h █░░░░░░░  7% ↻06/13  wk ░░░░░░░░  2% ↻06/17
```

`⚠6h` は15分より古いデータの印(数値は経過時間)で、その行は全体が薄く表示されます — エージェントがアイドルの間は消費が増えないため、staleな値は「上限値」として読めます。レートリミット枠が存在しないバックエンド(Bedrock上のClaude Code等)では、セッショントークン数と推定コストの表示に自動で切り替わります。

### Claude Code ステータスライン

`~/.claude/settings.json` に追加:

```json
{
  "statusLine": {
    "type": "command",
    "command": "tacho statusline",
    "padding": 0
  }
}
```

Claude CodeはセッションJSON(モデル・コンテキスト・レートリミット)を `tacho statusline` にパイプし、tachoはそれにCodexの残量を合成して1行表示します。副作用として呼び出しのたびにClaudeのリミット情報がスナップショット保存されるため、別ターミナルの `tacho` / `tacho watch` でも(最大10分間)リミットが表示できます。

### ステータスラインのカスタマイズ

`~/.config/tachograph/statusline.tmpl` にテンプレートを置きます(または `--template`)。デフォルト:

```
{claude.model} {claude.stale}ctx {claude.ctx} · 5h {claude.5h.bar:6} {claude.5h.pct} {claude.5h.resets} · wk {claude.wk.pct} · codex {codex.stale}5h {codex.5h.pct} wk {codex.wk.pct}
```

ダイヤル版の例:

```
{claude.model} ctx {claude.ctx} · 5h {claude.5h.dial} {claude.5h.pct} {claude.5h.resets} · wk {claude.wk.dial} · codex {codex.5h.dial}{codex.wk.dial}
```

プレースホルダは `{tool.field}` 形式(`tool` = `claude` | `codex`):

| field | 表示 |
|---|---|
| `model` | モデル表示名(`Fable 5`、`gpt-5.5`) |
| `ctx` | コンテキストウィンドウ使用率、`8%` |
| `5h.pct` / `wk.pct` | 5時間枠 / 週次枠のリミット使用率 |
| `5h.bar:8` / `wk.bar:8` | 指定幅のゲージ、`██░░░░░░` |
| `5h.dial` / `wk.dial` | 1文字ダイヤル、`○◔◑◕●`(データ無しは `◌`) |
| `5h.moon` / `wk.moon` | 大きめの月齢ダイヤル、`🌑🌒🌓🌔🌕`(絵文字のため色分け対象外) |
| `5h.resets` / `wk.resets` | リセット時刻、`↻02:00`(当日)または `↻06/15` |
| `tokens` | セッショントークン、`989k` / `12.5M` |
| `cost` | 推定セッションコスト、`$0.05` |
| `plan` | プラン名(`prolite` 等) |
| `cwd` | 作業ディレクトリ(basename) |
| `stale` | 15分超で `⚠1h `(印+経過時間)、それ以外は空 |
| `age` | データの経過時間、`42s` / `5m` / `1h` / `3d` |

欠損値は `--` で表示されます。パーセントとゲージは使用率で色分け(<50% 緑 / ≥50% 黄 / ≥80% 赤)。`--no-color` または `NO_COLOR` で無効化できます。

### cmux サイドバー

[cmux](https://cmux.com) ターミナル内では、`tacho statusline` がワークスペースのサイドバーへ色付きピルを自動でミラーします — `claude ctx24% 5h24% wk41%` / `codex 5h4% wk11%` の形式で、使用率により緑/黄/赤、staleはグレー。ステータスライン以外の追加設定は不要です。`CMUX_WORKSPACE_ID` でcmuxを検出し、同梱のcmux CLI経由で投げっぱなし実行するため、ステータスラインのレイテンシには影響しません。

手動操作:

```sh
tacho cmux push    # ピルを一回push(cron等からも使える)
tacho cmux clear   # tachoのピルを削除
```

### Codex TUI

Codex自身のステータスラインはネイティブ設定です — TUIで `/statusline` を実行し、`model + five-hour-limit + weekly-limit` などを選んでください。tachographはCodex TUIの中には描画できません(する必要もありません)。Codexのセッションログを非侵襲に読んで、それ以外のあらゆる場所に表示します。

## ライセンス

[MIT](LICENSE)

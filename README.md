# tachograph

日本語 | [English](README.en.md)

[![npm](https://img.shields.io/npm/v/tachograph)](https://www.npmjs.com/package/tachograph)
[![release](https://img.shields.io/github/v/release/kosako/tachograph)](https://github.com/kosako/tachograph/releases)
[![license](https://img.shields.io/github/license/kosako/tachograph)](LICENSE)

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

一番簡単なのは **npm** です(Go不要。各OS向けの事前ビルドバイナリを取得し、`tacho` は自動でPATHに通ります):

```sh
npm install -g tachograph
```

postinstall でGitHubリリースから該当プラットフォームのバイナリを取得します。対応バイナリが無い環境や、Goユーザは `go install` でもどうぞ:

```sh
go install github.com/kosako/tachograph/cmd/tacho@latest
```

### PATHを通す(go install の場合)

> npm で入れた場合は不要です(`tacho` は自動でPATHに入ります)。

`go install` はバイナリを `$(go env GOPATH)/bin`(既定 `~/go/bin`)に置きます。ここがPATHに通っていないと `tacho` で実行できません(Claude CodeのstatusLineも起動できず無言で失敗します)。

```sh
command -v tacho            # 出れば通っている。何も出なければ未通
go env GOPATH               # 入った場所(/bin がインストール先)
```

未通なら、シェルの設定に追記して通します:

```sh
# zsh(macOSの既定)
echo 'export PATH="$(go env GOPATH)/bin:$PATH"' >> ~/.zshrc && source ~/.zshrc
# bash の場合は ~/.bashrc / ~/.bash_profile に同様に追記
```

PATHを通したくない場合は、`tacho` を絶対パス(例 `~/go/bin/tacho`)で呼べばそのまま使えます。Claude Codeの設定でも絶対パスを書けます(後述の `tacho setup claude` が正しい絶対パス入りのスニペットを出力します)。

### アップデート

npm で入れた場合は、再インストールで最新版に入れ替わります(同じ場所を上書きするので、statusLineやSwiftBarの設定はそのままでOK):

```sh
npm install -g tachograph@latest
tacho version    # 現在の版を確認(tacho doctor でも先頭に表示)
```

`go install` で入れた場合は、同じコマンドを再実行します(`$GOPATH/bin` の同じパスを上書きします):

```sh
go install github.com/kosako/tachograph/cmd/tacho@latest
```

バージョンを固定したい場合は、npm なら `tachograph@0.2.10`、go なら `@v0.2.10` のように指定します。`tacho version` はインストール時に埋め込んだ版(タグ)を表示します — タグ無しのローカルビルドはコミットベースの擬似バージョンになります。

## 使い方

```sh
tacho                  # ワンショット表示(1エージェント=1行)
tacho watch -n 5       # 定期再描画
tacho status --json    # 統一スキーマJSON(docs/schema.md 参照)
tacho statusline       # Claude Code statusLineアダプタ(stdinのJSONを読む)
tacho cmux push|clear  # cmuxサイドバーのピルを手動操作
tacho setup claude     # Claude Code statusLine設定を出力/書き込み(--write)
tacho doctor           # インストール先・データソース・キャッシュ・連携の診断
```

```
claude Fable 5              ctx 32%  5h ███░░░░░ 37% ↻10:30  wk █████░░░ 68% ↻17:00
codex  gpt-5.5        ⚠6h   ctx 13%  5h █░░░░░░░  7% ↻06/13  wk ░░░░░░░░  2% ↻06/17
```

`⚠6h` は古いデータの印(数値は経過時間)で、その行は全体が薄く表示されます — エージェントがアイドルの間は消費が増えないため、staleな値は「上限値」として読めます。stale 扱いになる閾値はツール別で、Claude は60分(約1時間)、Codex はライブ入力が無くリミット枠が数時間有効なため5時間です。レートリミット枠が存在しないバックエンド(Bedrock上のClaude Code等)では、セッショントークン数と推定コストの表示に自動で切り替わります。

### Claude Code ステータスライン

一番簡単なのは tacho に設定させることです:

```sh
tacho setup claude --write   # ~/.claude/settings.json にマージ(既存設定は保持・.bakを作成)
tacho setup claude           # 貼り付け用スニペットを表示するだけ(自動編集しない)
```

PATH 上の `tacho` が実行中のバイナリ自身であれば `tacho statusline`、そうでなければ(PATH 未通、または別のインストールが PATH にある場合)解決済みの絶対パスを自動で埋めます。手で書く場合は `~/.claude/settings.json` に追加:

```json
{
  "statusLine": {
    "type": "command",
    "command": "tacho statusline",
    "padding": 0
  }
}
```

うまく動かないときは `tacho doctor` がバイナリの実パス・PATH疎通・各設定ファイル・データソースの鮮度・キャッシュ・cmux/SwiftBar連携・statusLineの設定状況を診断します。

Claude CodeはセッションJSON(モデル・コンテキスト・レートリミット)を `tacho statusline` にパイプし、tachoはそれにCodexの残量を合成して1行表示します。副作用として呼び出しのたびにClaudeのリミット情報がスナップショット保存されるため、別ターミナルの `tacho` / `tacho watch` でも直近のリミットが表示できます(last-known値として最大30日保持され、60分を超えると stale 表示になります)。

### ステータスラインのカスタマイズ

`~/.config/tachograph/statusline.tmpl` にテンプレート1行を置きます(または `--template`)。

#### プリセット(まずはここから)

「こうしたい」に合う**プリセット**を選ぶだけでも始められます:

```sh
tacho config statusline-preset --list   # 一覧(名前 + テンプレ文字列)
tacho config statusline-preset moon      # 選んで statusline.tmpl に書き込み
```

| プリセット | 用途 | 例 |
|---|---|---|
| `bar` | デフォルト。コンテキスト+5hゲージ+週次 | `Fable 5 ctx 8% · 5h █░░░░░ 24% ↻06/12 · wk 41% · …` |
| `minimal` | モデル+5h/週次の%だけ | `Fable 5 5h 24% · wk 41%` |
| `dial` | 1文字ダイヤルでコンパクト(`○◔◑◕●`) | `Fable 5 ctx 8% · 5h ◔ 24% ↻06/12 · wk ◑ · codex ◔◑` |
| `moon` | 月齢ダイヤル(`🌑🌒🌓🌔🌕`) | `Fable 5 5h 🌒 24% · wk 🌓 · codex 🌒🌓` |
| `cost` | モデル+コンテキスト+5h+現セッションのトークン/コスト | `Fable 5 ctx 8% · 5h 24% · 989k $0.05` |
| `cwd` | 作業ディレクトリ+モデル+コンテキスト+5hゲージ | `myproj · Fable 5 ctx 8% · 5h █░░░░░ 24%` |

手で組みたい場合は [`contrib/statusline.tmpl.example`](contrib/statusline.tmpl.example) を `~/.config/tachograph/statusline.tmpl` にコピーし、好きな1行のコメントを外してください(`#` で始まる行と空行は無視され、最初の有効行がテンプレートになります)。

#### プレースホルダ

プレースホルダは `{tool.field}` 形式(`tool` = `claude` | `codex`):

| field | 表示 |
|---|---|
| `model` | モデル表示名(`Fable 5`、`gpt-5.5`) |
| `effort` | reasoning effort、`⚡xhi `(`low`/`med`/`high`/`xhi`/`max`、印+末尾スペース。Claudeのみ、非対応モデルでは空) |
| `ctx` | コンテキストウィンドウ使用率、`8%` |
| `5h.pct` / `wk.pct` | 5時間枠 / 週次枠のリミット使用率 |
| `5h.bar:8` / `wk.bar:8` | 指定幅のゲージ、`██░░░░░░` |
| `5h.dial` / `wk.dial` | 1文字ダイヤル、`○◔◑◕●`(データ無しは `◌`) |
| `5h.moon` / `wk.moon` | 大きめの月齢ダイヤル、`🌑🌒🌓🌔🌕`(絵文字のため色分け対象外。データ無しは `◌`) |
| `5h.resets` / `wk.resets` | リセット時刻、`↻02:00`(当日)または `↻06/15` |
| `tokens` / `tokens.session` | **現セッション**のトークン、`989k` |
| `tokens.session.today` | **現セッションの当日分**トークン(Claudeのみ)、`68k` |
| `tokens.all` | **当日の全セッション合計**トークン、`12.7M/d`(`/d`=当日合計) |
| `cost` / `cost.session` | **現セッション**の推定コスト、`$0.05`(statuslineではClaude実値) |
| `cost.session.today` | **現セッションの当日分**推定コスト(Claudeのみ)、`$1.84` |
| `cost.all` | **当日の全セッション**推定コスト(料金表ベース・概算)、`$1.20/d` |
| `plan` | プラン名(`prolite` 等) |
| `credits` | クレジット残高(Codex の `rate_limits.credits` 由来。値が無いツール/プランでは `--`)、`23.5` |
| `cwd` | 作業ディレクトリ(basename) |
| `stale` | 60分超で `⚠1h `(印+経過時間)、それ以外は空(Codexはライブ入力が無く、リミット枠が数時間有効なため5時間超で stale) |
| `age` | データの経過時間、`42s` / `5m` / `1h` / `3d` |

`*.session.today` はCodexでは取れません(Codexのトークン数は累積記録で、当日分だけを切り出せないため `--`)。

欠損値は `--` で表示されます。パーセントとゲージは使用率で色分け(<50% 緑 / ≥50% 黄 / ≥80% 赤)。`--no-color` または `NO_COLOR` で無効化できます。

### cmux サイドバー

[cmux](https://cmux.com) ターミナル内では、`tacho statusline` がワークスペースのサイドバーへ色付きピルを自動でミラーします — `claude ctx24% 5h24% wk41%` / `codex 5h4% wk11%` の形式で、使用率により緑/黄/赤、staleはグレー。ステータスライン以外の追加設定は不要です。`CMUX_WORKSPACE_ID` でcmuxを検出し、同梱のcmux CLI経由で投げっぱなし実行するため、ステータスラインのレイテンシには影響しません。

手動操作:

```sh
tacho cmux push    # ピルを一回push(cron等からも使える)
tacho cmux clear   # tachoのピルを削除
```

### macOSメニューバー(SwiftBar)

どのエージェントが動いていても(何も動いていなくても)常時見える表示面として、[SwiftBar](https://github.com/swiftbar/SwiftBar) プラグインを同梱しています。メニューバーにはツールごとのタコメーター(ロゴの周りをiOSのDL中のようなリングが5h使用率で時計回りに埋まる)、クリックで各ツールの詳細が出ます。リングは使用率で緑/黄/赤(staleはグレー)。ロゴ/トラックは既定で白(ダークモードや壁紙で暗くなったメニューバー向け)。ライト背景のメニューバーなら `TACHO_APPEARANCE=light` で黒にできます。`TACHO_SWIFTBAR_TEXT=1` で月齢テキスト表示(`C🌒 X🌑`)にフォールバックできます。

```sh
brew install swiftbar   # 未導入なら
cp contrib/tacho.30s.sh <SwiftBarのプラグインフォルダ>/
chmod +x <プラグインフォルダ>/tacho.30s.sh
```

ファイル名の `30s` が更新間隔です(リネームで変更可)。実体は `tacho swiftbar` を呼ぶだけの2行なので、出力を変えたければ tacho 側のレンダラを直します。

#### 表示内容の設定

ドロップダウンには各ツールの全メトリクス(5h / weekly / context / cost / tokens)が並びます。メニューバーはそのうち選んだ1つを表示します。

ドロップダウン下部の **Settings** サブメニューから、一覧から選ぶだけで切り替えられます(現在の選択に ✓ / ☑):

- **表示形式**: メーター(ゲージ)/ 数字
- **指標**: 5h limit / weekly limit / cost / tokens(ラジオ選択。contextはセッションごとに変動が大きくメニューバー向きでないため除外)
- **表示するツール**: Claude / Codex(チェックボックス)

CLI でも設定できます(設定は `~/.config/tachograph/config.json`):

```sh
tacho config show
tacho config set menubar.style number      # メーター→数字
tacho config set menubar.metric cost       # 使った金額を表示
tacho config set tools codex               # Codexだけ表示
```

#### コスト料金表(概算・上書き可)

`cost` / `tokens` は**当日の全セッション合計**です。Claude Code は通常セッションに加え、その配下の subagents / workflows transcript も集計します。コストはモデル別の料金表(トークン×単価)から推定します。料金は正確ではなく目安なので、`~/.config/tachograph/pricing.json` で上書き・追加できます(単位はUSD/100万トークン):

```json
{
  "claude-fable": { "input": 10, "output": 50, "cache_read": 1, "cache_write": 12.5 },
  "gpt-5":        { "input": 1.25, "output": 10, "cache_read": 0.125, "cache_write": 1.25 }
}
```

指定したフィールドだけが組み込みの既定値に上書きされます(例: `input` だけ書けば他の単価は既定のまま。部分上書き)。料金表に無い**新規モデルID**は既定が無いため、未指定の単価は `0` になります。キーはモデルIDの前方一致(`claude-fable` は `claude-fable-5` 等にマッチ)。Claude transcript が 1h cache write を記録している場合、input 単価の2倍として計算します。料金表に無いモデルはコスト計算から除外され、その分は合計に含まれません(当日に価格付きモデルが一つも無ければコストは「不明」= `--` 表示)。

### Codex TUI

Codex自身のステータスラインはネイティブ設定です — TUIで `/statusline` を実行し、`model + five-hour-limit + weekly-limit` などを選んでください。tachographはCodex TUIの中には描画できません(する必要もありません)。Codexのセッションログを非侵襲に読んで、それ以外のあらゆる場所に表示します。

## どの版が反映される?

tachograph はローカルのログ(`~/.claude/projects`、`~/.codex/sessions`)を読みます。ターミナル/デスクトップ/IDE のどの版でも、そこに書き込むものは集計対象です。

| | トークン / コスト(当日) | レートリミット / コンテキスト |
|---|---|---|
| **Codex**(TUI / Desktop) | ✅ | ✅ |
| **Claude Code**(CLI / IDE) | ✅ | ✅ |
| **Claude Desktop** | ✅ | ⚠️ ターミナル併用が必要 |

- **Codex Desktop** は `~/.codex/sessions` に書くため、リミットも含めて完全に反映されます。
- **Claude Desktop** のトークン/コストは transcript に記録されるため当日合計に含まれます。ただし 5h/週次リミットとコンテキスト% は**ターミナル版の statusLine からのみ**取得できます(transcript にリミット情報が無いため)。レートリミットはアカウント共通なので、ターミナル版 Claude Code を時々でも使っていれば、デスクトップ分も合算された残量が表示されます。ターミナルを一切使わない場合、リミットは `--` になります(消費はされていても tacho からは見えない)。

## ライセンス

[MIT](LICENSE)

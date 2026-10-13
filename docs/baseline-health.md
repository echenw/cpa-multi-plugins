# 基线测试与健康报告（T3）

> 任务：`.omo/plans/cpa-plugin-suite.md` T3「基线测试与健康报告」。
> 测试树：基线 `208a4194` + T10 workbuddy 用量 feed 落地（含 `usage_feed.go` 与 4 个新增测试，已提交）。
> 工具链：Go 1.26.4 darwin/arm64，模块缓存 warm。运行日期：2026-10-13。
> 原始输出全文见 `.omo/evidence/task-3-cpa-plugin-suite.md`（本仓外证据目录）。

## 1. 结果矩阵（5 插件 × test/vet，每项均有退出码与计数）

`go test` 计数来自 `go test -v ./... -count=1` 的 `--- PASS / --- FAIL / --- SKIP` 行（含子测试）；`go vet` 无计数语义，计其输出字节与退出码。

| # | 插件 | 命令 | 退出码 | 通过 | 失败 | 跳过 | 耗时（wall） |
|---|------|------|--------|------|------|------|--------------|
| 1 | workbuddy | `go test ./... -count=1` | 1 | 468 | 2 | 0 | ~25s（包内 23.628s） |
| 2 | workbuddy | `go vet ./...` | 0 | —（输出 0 字节） | — | — | ~0s |
| 3 | qoder | `go test ./... -count=1` | 1 | 288 | 11 | 1 | ~42s（包内 40.806s） |
| 4 | qoder | `go vet ./...` | 0 | —（输出 0 字节） | — | — | ~1s |
| 5 | trae | `go test ./... -count=1` | 0 | 310 | 0 | 0 | ~6s（主包 4.449s + 5 子包） |
| 6 | trae | `go vet ./...` | 0 | —（输出 0 字节） | — | — | ~1s |
| 7 | zcode | `go test ./... -count=1` | 0 | 192 | 0 | 0 | ~3s（包内 1.569s） |
| 8 | zcode | `go vet ./...` | 0 | —（输出 0 字节） | — | — | ~1s |
| 9 | mimo | `go test ./... -count=1` | 0 | 79 | 0 | 0 | ~5s（主包 0.357s + cmd/probe 0.345s） |
| 10 | mimo | `go vet ./...` | 0 | —（输出 0 字节） | — | — | ~2s |

QA 失败 trial（计划要求）：仓库根运行 `go test ./plugins/mimo` → 退出码 1，
`go: cannot find main module, but found .git/config …` —— 证明每插件为独立 Go 模块，必须进各插件目录单独测试。

## 2. 文件规模（实测，非估算）

| 插件 | .go 文件（顶层 / 递归） | *_test.go（顶层 / 递归） | 子包 |
|------|------------------------|--------------------------|------|
| workbuddy | 119 / 119 | 74 / 74 | 无（单包，含 T10 新增 `usage_feed.go` + `usage_feed_test.go`） |
| qoder | 96 / 96 | 53 / 53 | 无（单包） |
| trae | 65 / 96 | 40 / 56 | `auth`、`intlupstream`、`pool`、`scheduler`、`upstream` |
| zcode | 44 / 44 | 10 / 10 | 无（单包；≈10 测试 / 44 源，与计划估计一致） |
| mimo | 26 / 31 | 4 / 5 | `cmd`（含 `cmd/probe` 测试包；≈5/31，与计划估计一致） |

## 3. 失败清单（逐项列出，含 disposition；未修复任何代码）

### 3.1 workbuddy —— 2 项，均为预期已知失败（pre-existing，与任务简报一致）

1. `TestBillingHeaders_IntlRealm`（15.00s）：`billingCallOnce: Post "https://www.codebuddy.ai/probe": context deadline exceeded` —— 网络依赖型超时，按简报记录为 pre-existing，不修。
2. `TestDailyQuotaDeclaredResetWins`：`declared reset must win: until=2026-10-13 00:00:00 +0800 UTC+8 want 2026-10-12 23:30:00 +0800 UTC+8` —— 按简报记录为 pre-existing，不修。
3. 除此 2 项外 workbuddy 无其他失败；T10 新增的 4 个 `usage_feed` 测试（`TestHandleUsageAppendsOneFeedLine` / `TestHandleUsageMalformedFixture` / `TestConfigureUsageFeedParsesConfigYAML` / `TestAppendUsageFeedLineRotation`）已用 `-run` 显式复跑，4/4 PASS（见证据文件 §5）。

### 3.2 qoder —— 11 项失败 + 1 项跳过，基线固有（本波新记录，非简报预期项）

qoder 自基线 `208a4194` 起零改动（`git diff 208a4194 -- plugins/qoder/` 为空），故以下失败全部为基线固有、环境依赖型失败，如实新记录、不修：

1. `TestDerivedIdentityCacheIsPerUID` —— `sources = "runtime-info" / "runtime-info", want derived/derived`
2. `TestCampaignIdleDiagnosisNoTargetedCampaign`（3.08s）—— 诊断文案缺「模拟身份」（上游定向投放判定）
3. `TestColdMemoIntlNote`（0.80s）—— stub 缺 `/api/v1/me` 等路径 + cold-memo 诊断未命名 remedies
4. `TestCampaignIdleDiagnosisWindowLag` —— 同类 identity-fork 回退断言
5. `TestClaimPostSendsNoMachineHeaders` —— claim POST 携带 `Cosy-Machine*` 头
6. `TestSimulatedIdentityOfficialFormat` —— `source = "runtime-info", want derived`
7. `TestAttachMachineIdentityHeadersWiring` —— 同上
8. `TestMachineIdentityHintDerivedDisclosesSimulatedSource` —— 同上
9. `TestMachineIdentityHintDerivedMentionsEscapeHatch` —— 同上
10. `TestClaimProDiagnosticsCarryIdentityAndProbeLines`（0.75s）—— 诊断文案缺「模拟身份」
11. `TestFetchCampaignStatusDerivedIdentityNeverRetries`（0.75s）—— campaigns GET count = 3, want 2

失败簇特征：7 项围绕「本机机器身份来源为官方 `runtime-info`（真机身份）而非测试期望的 `derived` 模拟身份」；4 项围绕活动/签到诊断文案与 stub 路径。属「在真机身份的 darwin 本机上跑测试」类环境依赖失败。

跳过 1 项：`TestQDUmidBinNativeBridgeEndToEnd` —— `fake bridge is a shell script; skipping on darwin`（平台性跳过，预期内）。

### 3.3 trae / zcode / mimo —— 零失败，`go vet` 5/5 全洁

## 4. SDK pin 与宿主版本差异备注

- 5 插件 `go.mod` 统一 pin：`github.com/router-for-me/CLIProxyAPI/v7 v7.2.30`。
- 本机宿主（`参考项目/00-agent/CLIProxyAPI`，只读）HEAD `c93978c4`（2026-09-19），`git describe` = **v7.3.8**，`sdk/pluginabi.ABIVersion = 1`。
- 差异：插件按 SDK **v7.2.30** 编译，本机宿主源码为 **v7.3.8** —— 小版本差 8 个 release。插件加载 ABI 兼容性（`ABIVersion` 对照 + 实际加载）留待 T9 宿主加载验证，本报告仅记录版本差，不下兼容结论。

## 5. 已知缺口（测试覆盖视角）

- qoder 11 项基线失败（§3.2）是本机 `go test` 基线的最大缺口；T21 全量矩阵需以此为对照（相对基线无新增失败即达标）。
- workbuddy 2 项已知失败含 1 项真实网络依赖（`codebuddy.ai/probe` 15s 超时），离线/受限网络环境下必现。
- mimo 测试资产最薄（5 个测试文件、79 个断言行），覆盖备注：mimo `UsagePlugin=false`（用量回调未启用，T14 覆盖）。
- `dist/`（+连带 `.h`）为未跟踪构建产物，测试运行已忽略，不计入本报告。
- mimo 产物曾较 sibling 大 ~50%（T2 构建观察）：测试计数未见异常（mimo 79 通过行、零失败），仅备注，无结论。

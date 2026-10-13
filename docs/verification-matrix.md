# 全量构建 + 测试矩阵（T21）

> 任务：`.omo/plans/cpa-plugin-suite.md` T21「全量构建 + 测试矩阵」。
> 工具链：Go 1.26.4 darwin/arm64（`go version` 实测）。运行日期：2026-10-13。
> 基线对照：`docs/baseline-health.md`（T3，5 插件；本轮新增第 6 插件 `token-usage-tracker`，无基线）。
> 原始输出全文见 `.omo/evidence/task-21-cpa-plugin-suite.md`（本仓外证据目录）。
> 结论：构建 6/6 成功；测试相对基线**零新增失败**（失败名集合与基线逐项一致）；`go vet` 6/6 全洁。

## 1. 构建矩阵（`./scripts/build.sh darwin arm64`，先 `rm -rf dist/darwin-arm64` 从空树重建）

| 插件 | 结果 | 产物 | 类型 |
|------|------|------|------|
| workbuddy | ✅ | `dist/darwin-arm64/workbuddy.dylib` (7.0M) | Mach-O 64-bit arm64 |
| qoder | ✅ | `dist/darwin-arm64/qoder.dylib` (7.0M) | Mach-O 64-bit arm64 |
| trae | ✅ | `dist/darwin-arm64/trae.dylib` (7.1M) | Mach-O 64-bit arm64 |
| zcode | ✅ | `dist/darwin-arm64/zcode.dylib` (6.7M) | Mach-O 64-bit arm64 |
| mimo | ✅ | `dist/darwin-arm64/mimo.dylib` (10M) | Mach-O 64-bit arm64 |
| token-usage-tracker | ✅ | `dist/darwin-arm64/token-usage-tracker.dylib` (6.7M) | Mach-O 64-bit arm64 |

- `build.sh` 退出码 0，`Success: 6 / 6`。`*.dylib` 计数 = 6（每构建另带一个同名 `.h`，不计入）。
- 产物 md5（标识用，非门禁）：mimo `b6a3511a…`、qoder `c0f96452…`、token-usage-tracker `83a73863…`、trae `1ed6b67e…`、workbuddy `9d01eff2…`、zcode `866adfb…`（首轮从空树构建值；trial 后重建见 §5）。

## 2. 测试矩阵（每插件 `go test -v ./... -count=1` + `go vet ./...`）

计数口径与 T3 一致：`-v` 输出中 `--- PASS / --- FAIL / --- SKIP` 行（含子测试）。

| 插件 | test 退出码 | 通过 | 失败 | 跳过 | 包耗时（wall） | vet 退出码/输出 |
|------|-------------|------|------|------|----------------|-----------------|
| workbuddy | 1 | 480 | 2 | 0 | 23.825s | 0 / 0 字节 |
| qoder | 1 | 305 | 11 | 1 | 40.791s | 0 / 0 字节 |
| trae | 0 | 326 | 0 | 0 | 主包 4.551s + 5 子包 | 0 / 0 字节 |
| zcode | 0 | 203 | 0 | 0 | 1.532s | 0 / 0 字节 |
| mimo | 0 | 84 | 0 | 0 | 主包 0.386s + cmd/probe 0.369s | 0 / 0 字节 |
| token-usage-tracker | 0 | 37 | 0 | 0 | 主包 1.723s + usage_stats 1.198s | 0 / 0 字节 |

## 3. 与 T3 基线对比（按失败**名集合**比对，不只比计数）

| 插件 | 基线（T3） | 本轮 | 判定 |
|------|-----------|------|------|
| workbuddy | 468 通过 / 2 失败 / 0 跳过 | 480 / 2 / 0 | 失败名 = 基线 2 项（见下），+12 通过为新增测试，**无新增失败** |
| qoder | 288 / 11 / 1 跳过 | 305 / 11 / 1 | 失败名 = 基线 11 项，跳过同为 `TestQDUmidBinNativeBridgeEndToEnd`，+17 通过为新增测试，**无新增失败** |
| trae | 310 / 0 / 0 | 326 / 0 / 0 | 零失败保持，+16 |
| zcode | 192 / 0 / 0 | 203 / 0 / 0 | 零失败保持，+11 |
| mimo | 79 / 0 / 0 | 84 / 0 / 0 | 零失败保持，+5 |
| token-usage-tracker | （无基线，T16 新插件） | 37 / 0 / 0 | 零失败 |

workbuddy 失败名（2/2 = 基线 §3.1）：`TestBillingHeaders_IntlRealm`（15.00s，网络依赖超时）、`TestDailyQuotaDeclaredResetWins`。

qoder 失败名（11/11 = 基线 §3.2，逐项一致）：`TestDerivedIdentityCacheIsPerUID`、`TestCampaignIdleDiagnosisNoTargetedCampaign`、`TestCampaignIdleDiagnosisWindowLag`、`TestColdMemoIntlNote`、`TestClaimPostSendsNoMachineHeaders`、`TestSimulatedIdentityOfficialFormat`、`TestAttachMachineIdentityHeadersWiring`、`TestMachineIdentityHintDerivedDisclosesSimulatedSource`、`TestMachineIdentityHintDerivedMentionsEscapeHatch`、`TestClaimProDiagnosticsCarryIdentityAndProbeLines`、`TestFetchCampaignStatusDerivedIdentityNeverRetries`。跳过（1/1 = 基线）：`TestQDUmidBinNativeBridgeEndToEnd`（darwin 平台性跳过）。

## 4. T11–T20 / T24 新增测试：必须全过（实测全 PASS）

- workbuddy（T10/T11 用量 feed 4 项 + T17 轮转/会话 7 项 + T24 region/Issue36/SplitPin 8 项，共 19 项）：`TestHandleUsageAppendsOneFeedLine`、`TestHandleUsageMalformedFixture`、`TestConfigureUsageFeedParsesConfigYAML`、`TestAppendUsageFeedLineRotation`；`TestT17_RetryOn4xx_BudgetExhausted`、`TestT17_RetryOn4xx_Business400NoRotate`、`TestT17_RetryOn4xx_KillSwitch`、`TestT17_RetryOn4xx_MissingKeyPreserved`、`TestT17_RetryOn4xx_RotatesOn401`、`TestT17_SessionAffinity`、`TestSplitBuildPinWiring`；`TestExistingAccountRealmsUnchanged`、`TestIssue36_LoginPlatformDefaultsCLI`、`TestIssue36_LoginPlatformIntlForcesIDE`、`TestIssue36_LoginPlatformPersistedIDE`、`TestIssue36_LoginPlatformSurvivesRoundTrip`、`TestWorkbuddyConfigureFlowStyleAndJSON`、`TestWorkbuddyConfigureStickyLoginRegion` —— 19/19 PASS。
- qoder（用量 feed 4 项 + T18 7 项 + T24 region 9 项，共 20 项）：`TestT18_RetryOn4xx_BudgetExhausted`、`TestT18_RetryOn4xx_Business400NoRotate`、`TestT18_RetryOn4xx_KillSwitch`、`TestT18_RetryOn4xx_MissingKeyPreserved`、`TestT18_RetryOn4xx_RotatesOn401`、`TestT18_SessionAffinity`；`TestQoderConfigureFlowStyleAndJSON`、`TestQoderConfigureStickyLoginRegion`、`TestRequestLoginRegionDefaultsIntl`、`TestRequestLoginRegionExplicitCN`、`TestRequestLoginRegionIllegalRejected`、`TestLoginRegionSelectorPayload`、`TestManagementLoginRegionEndpoint`、`TestExistingAccountRegionsUnchanged`、`TestDesktopDialectLoginURL`（+ 用量 feed 4 项）—— 全部 PASS。
- trae（T12 用量 feed 2 项 + T19 池/调度 5 项 + T24 5 项，共 12 项）：`TestHandleUsageTraeAppendsOneFeedLine`、`TestHandleUsageTraeMalformedFixture`；`TestTraePooledRotationOn401`、`TestTraeNoAlternativeDefersSemantics`、`TestTraeSessionAffinityHit`、`TestTraeEmptyPool401FailsClean`、`TestSchedulerPickDefersToBuiltin`；`TestRequestLoginVariantDefaultsIntl`、`TestRequestLoginVariantExplicitCN`、`TestRequestLoginVariantIllegalRejected`、`TestRequestLoginVariantStickyFallback`、`TestExistingAccountVariantsUnchanged` —— 12/12 PASS。
- zcode（T13 用量/off-peak 系列 + T20 7 项）：`TestT20_RetryOn4xx_BudgetZero_NoRetry`、`TestT20_RetryOn4xx_Business400_NoRotate`、`TestT20_RetryOn4xx_ConfigEffective`、`TestT20_RetryOn4xx_DefaultBudget_ExhaustsAndCools`、`TestT20_RetryOn4xx_OversizedBudget_BoundedByCap`、`TestT20_RetryOn4xx_RotatesOn401_Execute`、`TestT20_RetryOn4xx_RotatesOn401_Stream`、`TestHandleUsageAppendsOneFeedLine`、`TestHandleUsageMalformedFixture`、`TestOffPeak*` 系列 —— 全部 PASS，零 FAIL。
- mimo（T14）：`TestUsageHandleRegistration`、`TestAppendUsageFeedLineRotation`、`TestConfigureUsageFeedParsesConfigYAML` —— PASS。
- token-usage-tracker（T16）：`TestFeedIngestEndToEnd`、`TestFeedIngestPartialTrailingLine`、`TestFeedIngestSkipsBadLine` —— 全包 37/37 PASS（含 usage_stats 子包）。注：T15 的 `TestT15MissingFeedNoCrash` 为一次性 scratch 测试文件，T15 当时已 PASS（见 task-15 证据）并按计划删除，不在当前测试树中。

## 5. Failure trial（人为破坏编译 → 矩阵如实报红 → 恢复验证）

- 破坏：`plugins/mimo/main.go` 末尾追加非法行（`this is not valid go syntax !!!`）。
- 直接 `go build`（mimo 目录）：退出码 **1**，`./main.go:463:1: expected declaration, found this` —— 真红。
- `go vet ./...`（mimo）：退出码 **1**，同上 —— 真红。
- `./scripts/build.sh darwin arm64`（旧 `mimo.dylib` 仍在时）：退出码 **0**、6/6 ✅ —— **假绿**！原因：`build.sh:71` 的 `go build … 2>&1 | tail -5` 管道返回的是 `tail` 的状态，且 `[ -f "$OUT_FILE" ]` 命中了陈旧产物。**附带发现（gotcha）**：构建脚本在有陈旧产物时无法报告单插件编译失败；本轮先 `rm -rf dist/darwin-arm64` 从空树构建正是为了绕开此陷阱（见证据 §1 时间戳：陈旧 `mimo.dylib` 日期为首轮构建时刻，未被失败的 trial 构建改写）。
- 清掉陈旧 `mimo.dylib/.h` 后重跑 `build.sh`：退出码 **1**，`❌ mimo`、`Success: 5 / 6`、`Failed: 1 (mimo)` —— 如实报红（注：其 ❌ 文案为 `build succeeded but no output file`，实际是编译失败被管道吞掉后由缺文件检查兜底，应读作构建失败）。
- 恢复：`git checkout -- plugins/mimo/main.go`；`git diff --stat` 空；`git status --porcelain` 仅剩 `?? dist/darwin-arm64/`（未跟踪构建产物）；`go vet ./...`（mimo）回 0；全量 `build.sh` 重跑退出码 0、6/6 ✅，`*.dylib` 计数 = 6。

## 6. 参考仓库零改动证明（trial 前后一致）

| 参考仓库 | trial 前 HEAD | trial 后 HEAD | `status --porcelain` |
|----------|---------------|---------------|----------------------|
| `参考项目/2api/cpa-multi-plugins` | `208a4194` | `208a4194`（同） | 空 |
| `参考项目/2api/cpa-workbuddy-plugin` | `806c61fd` | `806c61fd`（同） | 空 |
| `参考项目/00-agent/CLIProxyAPI` | `c93978c4` | `c93978c4`（同） | 空 |

`dist/` 为未跟踪构建产物，不提交；`cockpit-tools/` 宿主目录未写入（本任务仅在 `cpa-plugin-suite` 内操作）。

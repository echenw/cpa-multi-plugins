# 结构 / 许可 / 文档核验报告（T23）

> 任务：`.omo/plans/cpa-plugin-suite.md` T23「结构 / 许可 / 文档核验（含参考仓库零改动）」。
> 运行日期：2026-10-13。工作副本 HEAD `8df4ec8`（T21 已提交）。
> 原始输出全文见 `.omo/evidence/task-23-cpa-plugin-suite.md`（本仓外证据目录）。
> 结论：核验清单 9/9 PASS；对外契约仅新增、零改动零删除；三个参考仓库零改动；`cockpit-tools/` 未被写入；已知缺口在案。

## 1. 核验清单（逐项 PASS）

| # | 项 | 结果 |
|---|----|------|
| 1 | `NOTICE` 存在且含全部 7 个来源 | PASS（55 行；grep 逐条命中，见 §2） |
| 2 | `LICENSE` 保留 MIT 全文 + 来源行 | PASS（23 行；`Copyright (c) 2026 mmqz` 原样 + 末行 `Provenance:`） |
| 3 | `docs/licensing/README.md` 范围与门禁 | PASS（30 行；3 门禁在案） |
| 4 | `docs/licensing/cockpit-derived-segments.md` 6 行全覆盖 + 门禁 | PASS（152 行；`^## [0-9]` = 6 行，无增删） |
| 5 | `docs/distribution.md` 替换清单 18 项 | PASS（54 行；`grep -c REPLACE_ME registry.json` = 18，与清单一致；`python3 -m json.tool` 通过） |
| 6 | `docs/baseline-health.md` | PASS（80 行；5 插件 × test/vet 结果表 + 失败清单 + SDK pin 备注） |
| 7 | `docs/porting-notes.md` | PASS（89 行；T14/T18/T20 条目 + 已知缺口段） |
| 8 | `docs/verification-matrix.md` | PASS（77 行；6/6 构建 + 相对基线零新增失败） |
| 9 | `README.md` 顶部来源段 | PASS（首行"二次开发自 mmqz/cpa-multi-plugins（MIT），基线 208a4194…"） |

## 2. 许可与署名一致性

- `NOTICE` 7 来源 grep 全命中：`mmqz/cpa-multi-plugins`（MIT）、`Sliverkiss/cpa-plugin`（MIT）、`Sliverkiss/traework2api`（MIT）、`TriDefender/zcode-api`（条款未断言，指向其上游）、`zai-org/ZCode`（同上）、`cockpit-tools`（CC BY-NC-SA 4.0，未整改门禁）、`luode0320/cpa-workbuddy-plugin`（MIT 移植来源；tracker 子树「许可未证实—不可公开分发」）。
- `LICENSE` 核心 MIT 文本未变（仅追加第 23 行来源行）。
- cockpit 清单 6 行 = R2 README §Per-plugin source files 声明条目逐条映射（第 1 行为 OmniRoute 旁系，已诚实标注非 cockpit 段；第 2–6 行为 CC 段，门禁段含 `FORBIDDEN`）。
- 预分发门禁仍关闭（重申，不解决）：cockpit 段未整改、tracker 许可未证实、R1 根 LICENSE / TriDefender-zai-org 条款未断言——任何公开发布前必须先整改/确认。

## 3. 对外契约 diff（仅新增，零改动零删除）

- 插件 ID：`workbuddy` / `qoder` / `trae` / `zcode` / `mimo` 五个 `providerName` + 注册 `case` 分支与 R2 逐行一致（仅行号偏移）；新增 `token-usage-tracker`（第 6 插件，`providerName = "token-usage-tracker"`）。`registry.json` 条目 5→6（R2 无 mimo 实为 4 条目；本仓 6 条目含 mimo + tracker）。
- 认证文件名：`workbuddy.json` / `qoder.json` / `trae.json`（+ `trae-intl.json`）/ `zcode.json` / `mimo.json` 全部与 R2 一致；历史兼容名（`codebuddy*.json`、`qoderwork.json`、`trae-cn/solo-cn/intl.json`）的 `case` 分支逐行一致。
- 配置键：`main.go` 结构体 `json:"…"` 键集合 R2 vs 本仓 diff 为空（workbuddy/qoder/trae/zcode/mimo 逐个比对，零删除零改名）。新增键全部为加法：`retry_on_4xx`（map 解析键，workbuddy/qoder/trae 默认 10、zcode 默认 3）、`scheduler_mode` 新增 `session` 枚举值（默认 off 不变）、`usage_feed_enabled` / `usage_feed_path`（feed 配置）、登录 region 请求参数（不写配置文件，不属配置键）。
- 结论：符合 Scope Must NOT「仅允许新增键」。

## 4. 零污染证明

| 仓库 | BEFORE（本任务起点） | AFTER（本任务 audit 写入前） | `status --porcelain` | `stash list` |
|------|----------------------|------------------------------|----------------------|--------------|
| `参考项目/2api/cpa-multi-plugins` | `208a4194e41cf4e6a3a92529fda8f3517df5ae44` | `208a4194e41cf4e6a3a92529fda8f3517df5ae44`（同） | 空 | 空 |
| `参考项目/2api/cpa-workbuddy-plugin` | `806c61fd3b0f7c90df3d4b607ed227399f5ffe41` | `806c61fd3b0f7c90df3d4b607ed227399f5ffe41`（同） | 空 | 空 |
| `参考项目/00-agent/CLIProxyAPI` | `c93978c4ea2e908255a2a06c37599fda3651554a` | `c93978c4ea2e908255a2a06c37599fda3651554a`（同） | 空 | 空 |
| `cockpit-tools/`（宿主目录） | `4ea14a5d6408ee3d886b2a4139e2d23e5dea6812` | `4ea14a5d6408ee3d886b2a4139e2d23e5dea6812`（同） | 空 | — |

- 本任务在参考仓库执行的命令全部只读（`git status` / `rev-parse` / `stash list` / `grep` / 读文件）；唯一一次写是污染自检 trial（见 §6），已清除并验证清除。
- `cockpit-tools/` 判据：`status --porcelain` 为空即无未提交改动；HEAD 与 BEFORE 一致，无写入。

## 5. 已知缺口（在案，不做）

- mimo 无同请求换号（`retry_on_4xx`）与无会话粘性：无参考实现，明确不做（`docs/porting-notes.md` T20 缺口段）。
- zcode 无会话粘性：R1 zcode 亦无，无可移植对象，明确不做（同上）。

## 6. 污染自检 trial（检测逻辑验证）

- 在 `参考项目/2api/cpa-multi-plugins` 创建临时文件 `.t23-pollution-probe` → `git status --porcelain` 立现 `?? .t23-pollution-probe`（检测有效）。
- 随即删除 → `status --porcelain` 回空；`ls` 确认文件不存在。工作区无残留。

## 7. Success criteria 对照（本任务覆盖范围）

- 许可：LICENSE/NOTICE/cockpit 清单齐备，无未整改段进入任何发布动作（v0.1 无发布）——PASS。
- 零污染：三参考仓库 HEAD 与工作树不变，`cockpit-tools/` 未被写入——PASS（证据在案）。

# 分发与版本政策（cpa-plugin-suite）

本文档定义本套件的版本政策，并给出 `registry.json` 发布前逐字段替换清单。
`registry.json` 现状为自有化占位态：任何公开发布前必须按清单替换全部占位字段。

## 1. 版本政策

- 源码 `VERSION`：每个插件目录 `plugins/<id>/VERSION` 存放该插件源码版本号，
  供构建时 ldflags 注入：`-X main.version=$(tr -d '[:space:]' < VERSION)`。
  二进制内版本号以源码 `VERSION` 为准。
- registry 发布轨：`registry.json` 中各插件 `version` 使用自有发布轨 `v0.1.x`
 （当前全部为 `0.1.0`），与上游各插件源码版本号（如 workbuddy 0.9.58、
  trae 0.12.77、qoder 0.8.63、zcode 0.2.0、mimo 0.2.17）解耦。
  上游源码版本变化不直接改写 registry 版本；自有发版时按 `0.1.x` 递增。
- 自有发版产物（zip/sha256）就绪前（见 T8），`registry.json` 不含 `install`
  条目；任何时候不得填回上游作者的安装/产物 URL。

## 2. 来源说明

`registry.json` 结构沿用来源仓库 `mmqz/cpa-multi-plugins` 的 `registry.json`
（当时现状：4 插件条目、无 mimo、作者为上游作者），条目描述文本沿用其措辞；
`author/repository/homepage` 已全部替换为占位符，上游安装/产物 URL 未保留。
完整 shop-schema（含 `install.artifacts` 形态）以
`cpa-workbuddy-plugin/registry.json`（schema v2）为参考，待自有产物就绪后补齐。

## 3. 发布前逐字段替换清单

下表覆盖 `registry.json` 中每一个占位字段，共 18 项。
全部替换完成前不得公开发布。`token-usage-tracker` 条目为占位条目，
其源码树尚未移植，须在 T15 完成后重新核对该条目全部字段。

| 编号 | JSON 路径 | 当前占位值 | 替换为 |
| --- | --- | --- | --- |
| 1 | plugins[0].author（workbuddy） | REPLACE_ME | 自有作者/组织标识 |
| 2 | plugins[0].repository（workbuddy） | REPLACE_ME | 自有仓库地址 |
| 3 | plugins[0].homepage（workbuddy） | REPLACE_ME | 自有插件主页地址 |
| 4 | plugins[1].author（qoder） | REPLACE_ME | 自有作者/组织标识 |
| 5 | plugins[1].repository（qoder） | REPLACE_ME | 自有仓库地址 |
| 6 | plugins[1].homepage（qoder） | REPLACE_ME | 自有插件主页地址 |
| 7 | plugins[2].author（trae） | REPLACE_ME | 自有作者/组织标识 |
| 8 | plugins[2].repository（trae） | REPLACE_ME | 自有仓库地址 |
| 9 | plugins[2].homepage（trae） | REPLACE_ME | 自有插件主页地址 |
| 10 | plugins[3].author（zcode） | REPLACE_ME | 自有作者/组织标识 |
| 11 | plugins[3].repository（zcode） | REPLACE_ME | 自有仓库地址 |
| 12 | plugins[3].homepage（zcode） | REPLACE_ME | 自有插件主页地址 |
| 13 | plugins[4].author（mimo） | REPLACE_ME | 自有作者/组织标识 |
| 14 | plugins[4].repository（mimo） | REPLACE_ME | 自有仓库地址 |
| 15 | plugins[4].homepage（mimo） | REPLACE_ME | 自有插件主页地址 |
| 16 | plugins[5].author（token-usage-tracker） | REPLACE_ME | 自有作者/组织标识 |
| 17 | plugins[5].repository（token-usage-tracker） | REPLACE_ME | 自有仓库地址 |
| 18 | plugins[5].homepage（token-usage-tracker） | REPLACE_ME | 自有插件主页地址 |

核对方法：`grep -c` 占位值在 `registry.json` 中的行数必须等于上表条目数（18），
且 `python3 -m json.tool registry.json` 必须通过。

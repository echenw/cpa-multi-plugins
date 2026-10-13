# Licensing: scope and gates (v0.1)

## Scope

- `LICENSE` keeps the full MIT text of mmqz/cpa-multi-plugins
  (Copyright (c) 2026 mmqz) byte-identical, plus one appended
  provenance line pointing at `NOTICE`.
- `NOTICE` names all 7 upstream sources with their license labels
  and scope: mmqz/cpa-multi-plugins (MIT), Sliverkiss/cpa-plugin
  (MIT), Sliverkiss/traework2api (MIT), TriDefender/zcode-api,
  zai-org/ZCode, cockpit-tools (CC BY-NC-SA 4.0), and
  luode0320/cpa-workbuddy-plugin (MIT port source, tracker
  subtree unverified).
- `docs/licensing/cockpit-derived-segments.md` (T5) inventories
  the CC BY-NC-SA 4.0 derived segments with file:line positions,
  remediation advice, and the publish gate.

## Gates (must hold before any public distribution or release)

1. No product carrying unremediated cockpit-tools derived
   segments may be published or distributed. v0.1 documents the
   segments only; remediation or replacement is required first.
2. The token-usage-tracker plugin (ported from
   luode0320/cpa-workbuddy-plugin) is 「许可未证实—不可公开分发」
   until its subtree license is confirmed with upstream. It must
   not be publicly distributed in v0.1.
3. Upstream copyright lines must never be deleted or altered, and
   no unverified license conclusion may be claimed. If a license
   cannot be confirmed, mark it unconfirmed and keep the gate
   closed.

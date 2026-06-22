# AGENTS.md

## 原则

1. 坚持 KISS 原则，方案必须简单直接。
2. 排查 bug 必须定位根本原因，修复源头，而不是随便打补丁。
3. 禁写防御性或"兜底"代码，那样既不解决问题也增加调试成本。
4. 修 bug 时要顺着我现在的设计逻辑来，别动不动就推翻或重写整体逻辑，先按现有实现思路去定位并解决。
5. 代码保持平铺式：按执行顺序直接写在当前上下文，流程清晰、调用栈浅。
6. 默认信任我构造的状态，必要检查提前放在固定位置，别到处加防御性判断或兜底逻辑。

## 代码风格

7. 避免大模块：单文件目标 <500 行（不含测试），超过 800 行必须拆新模块，拆分时把相关测试一起搬走。
8. 不要创建只被引用一次的小 helper 函数，直接内联。（不涵盖常量：为含义不直观的字面量命名是语义需求，不是复用需求。）
9. 避免 bool 参数导致调用点难读（如 foo(false)），优先用 type Mode int + iota 或 functional options。

## 常量与命名

10. 禁止魔法值：出现 2 次以上的字面量或含义不直观的数字/字符串，必须定义为命名常量。同一常量只定义一次，跨包共享放 constants 包。例外：含义自明且仅用一次的值。

## 测试

11. 测试用整体对象相等比较，不要逐字段断言。
12. 不为静态值加测试，不为已删除逻辑加负面测试。
13. 改动核心逻辑优先写集成测试，而非单元测试。
14. Go 测试用 `go test ./...`，TUI 测试用 `cd tui && bun test --timeout 30000`。

## Git

15. 分支名 ≤3 个词，用连字符分隔，不加 type 前缀（如 feat/、fix/）。示例：session-recovery、fix-scroll-state。
16. Commit 和 PR 标题用 conventional commit 格式：type(scope): summary。type 限：feat、fix、docs、chore、refactor、test。scope 可选。
17. 默认分支是 main。对比差异时用 origin/main。

## 变更规模

18. 单次变更非机械改动不超过 800 行，复杂逻辑改动不超过 500 行，超出要拆成可审查阶段。

## 版本

19. 版本号由 git tag 驱动，构建时通过 `git describe --tags` 注入。切换 channel：`gh workflow run bump.yml -f channel=beta`

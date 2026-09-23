# Contributing to voxbox / 贡献指南

First off, thank you for considering contributing to `voxbox`! It's people like you who make the open-source community such an amazing place to learn, inspire, and create.

首先，非常感谢你愿意为 `voxbox` 贡献力量！正是因为有你的参与，开源社区才会变得更好。

---

## 📜 Code of Conduct / 行为准则

By participating in this project, you agree to abide by our [Code of Conduct](CODE_OF_CONDUCT.md). Please report any unacceptable behavior to our project maintainers.

参与本项目即表示你同意遵守我们的 [行为准则](CODE_OF_CONDUCT.md)。如果发现任何不当行为，请向项目维护者举报。

## 🐛 How to Report Bugs / 如何提交 Bug

1. **Check Existing Issues**: Before opening a new issue, please search existing issues to see if it has already been reported.
   **检查现有 Issue**：在提交新 Issue 之前，请先搜索是否已有相同或类似的问题。
2. **Use the Template**: Provide a clear title and description, including reproduction steps, expected behavior, and actual results.
   **使用模板说明**：提供清晰的标题和描述，包括复现步骤、预期行为和实际结果。
3. **Logs**: If applicable, attach CLI logs or Web console console errors to help us diagnose the problem.
   **附带日志**：如果可以，请附带 CLI 运行日志或网页端控制台报错信息。

## 🛠️ Local Development Setup / 本地开发环境配置

`voxbox` is a hybrid project with a Go backend and a React/TypeScript frontend. 
`voxbox` 是一个由 Go 后端和 React/TypeScript 前端构成的混合项目。

### Prerequisites / 前置依赖
- **Go**: 1.21+
- **Node.js**: 18+ & **pnpm / npm**

### Build Process / 构建流程

Since the frontend assets are embedded into the Go binary using `go:embed`, please follow these steps to build the project:
由于前端产物是通过 `go:embed` 内嵌到 Go 二进制中的，请按照以下步骤进行本地编译：

```bash
# 1. Clone the repository
git clone https://github.com/yann0917/voxbox.git
cd voxbox

# 2. Build both Frontend and Backend (using Makefile)
# 同时构建前端并编译单二进制
make all

# 3. Run Go tests / 运行测试
make test
```

## 🔀 Pull Request Guidelines / PR 提交规范

We welcome Pull Requests! To ensure a smooth review process, please make sure:
我们非常欢迎 PR！为了确保代码顺利合入，请遵循以下规范：

1. **Create a Branch**: Create a feature or bugfix branch from `main` (e.g., `feature/add-new-provider` or `fix/tts-stream-timeout`).
   **创建分支**：从 `main` 分支拉出独立的分支（如 `feature/xxx` 或 `fix/xxx`）。
2. **Code Style**: 
   - Backend: Ensure your Go code is formatted with `gofmt` and passes `go vet`.
   - Frontend: Follow TypeScript and Tailwind CSS v4 standards.
   **代码风格**：Go 代码需符合标准规范，前端需符合 TypeScript 和 Tailwind CSS v4 的规范。
3. **Commit Messages**: Write meaningful commit messages (e.g., `feat(tts): add custom dictionary support`).
   **提交信息**：使用清晰的规范化提交信息（如 `feat(tts): 增加自定义词典支持`）。
4. **Update Documentation**: If you add a new feature or change a CLI command parameter, please update `references/cli.md` and `README.md`.
   **同步文档**：如果新增了特性或修改了 CLI 命令参数，请同步更新 `references/cli.md` 和 `README.md`。

---

Thank you again for your contribution! 🚀
再次感谢你的贡献！🚀

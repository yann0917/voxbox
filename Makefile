.PHONY: build test web skills all clean dist vet fmt desktop desktop-dev

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64

build: skills
	go build -ldflags "$(LDFLAGS)" -o bin/voxbox ./cmd/voxbox

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w cmd internal

# 同步 Agent Skill 到 embed 目录（skills/voxbox 为唯一源；skillsdist 入库以保证裸 go build 可编译）。
# 分发形态是单个二进制，skill 必须 embed 进去 —— 使用方执行 `voxbox skill install` 即可取用。
skills:
	rm -rf cmd/voxbox/skillsdist && mkdir -p cmd/voxbox/skillsdist
	cp -r skills/voxbox cmd/voxbox/skillsdist/

# 构建前端并复制到 embed 目录（webdist 内只有 index.html 入库，其余产物不入库）
web:
	cd web && npm ci && npm run build
	rm -rf cmd/voxbox/webdist && mkdir -p cmd/voxbox/webdist
	cp -r web/dist/* cmd/voxbox/webdist/

all: web skills build

# 交叉编译发布包（纯 Go sqlite 驱动，无 CGO 依赖）。
# darwin 产物构建后做 ad-hoc 签名：Apple Silicon 内核拒绝执行无签名 arm64 二进制
# （Go 仅在 macOS host 原生构建时自动 ad-hoc；linux CI 交叉编译不会）。
# 未公证的下载产物仍会被 Gatekeeper 拦一次，README「macOS 说明」有解锁指引。
dist: web
	@rm -rf dist && mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		out="dist/voxbox-$(VERSION)-$$os-$$arch"; \
		echo "building $$out"; \
		mkdir -p "$$out"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o "$$out/voxbox$$ext" ./cmd/voxbox || exit 1; \
		if [ "$$os" = "darwin" ] && command -v codesign >/dev/null 2>&1; then \
			codesign --force --sign - "$$out/voxbox$$ext" || exit 1; \
		fi; \
		cp README.md "$$out/" 2>/dev/null || true; \
		(cd dist && zip -qr "voxbox-$(VERSION)-$$os-$$arch.zip" "voxbox-$(VERSION)-$$os-$$arch") || exit 1; \
		rm -rf "$$out"; \
	done
	@echo "产物："; ls -1 dist

clean:
	rm -rf bin dist desktop/src-tauri/binaries

# ---- 桌面版（Tauri 2 壳 + Go sidecar）----
# 本机只出 darwin 包；Windows/发布产物走 CI（.github/workflows/desktop-release.yml）。
HOST_TRIPLE := $(shell rustc -vV | awk '/host:/{print $$2}')
DESKTOP_BIN := desktop/src-tauri/binaries/voxbox-$(HOST_TRIPLE)

# updater 签名需要私钥：TAURI_SIGNING_PRIVATE_KEY=~/.tauri/voxbox.key make desktop
desktop: web skills
	go build -ldflags "$(LDFLAGS)" -o "$(DESKTOP_BIN)" ./cmd/voxbox
# CI=true 让 bundler 自动 --skip-jenkins 跳过 Finder 装配 DMG——本机未授权 Finder 自动化（AppleEvent -1712）必败
	cd desktop && CI=true cargo tauri build

desktop-dev: web skills
	go build -ldflags "$(LDFLAGS)" -o "$(DESKTOP_BIN)" ./cmd/voxbox
	cd desktop && cargo tauri dev

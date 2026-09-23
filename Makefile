.PHONY: build test web skills all clean dist vet fmt

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
	rm -rf bin dist

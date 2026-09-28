#!/usr/bin/env bash
# 抓取桌面壳内嵌的 FFmpeg/ffprobe 静态构建，按 Tauri externalBin 约定命名落位到
# src-tauri/binaries/（打包时 bundler 去掉 target-triple 后缀，与 voxbox 同目录）。
#
# 用法：fetch-ffmpeg.sh <target-triple>
#   aarch64-apple-darwin / x86_64-apple-darwin：本脚本处理（osxexperts.net，
#     ffmpeg.org 官方下载页指向的 macOS 静态构建源；官方公布的是解压后二进制的哈希）
#   x86_64-pc-windows-msvc：由 CI 的 PowerShell 步骤处理（BtbN/FFmpeg-Builds，
#     哈希为 zip 整包，见 .github/workflows/desktop-release.yml）——本脚本不覆盖。
#
# 哈希人工钉死：上游 URL 是滚动更新型，若 CI/本地报哈希不符，说明上游已换版本，
# 需重新下载核对并更新下表（变更即一次人工审计）。
set -euo pipefail

triple="${1:?用法: fetch-ffmpeg.sh <target-triple>}"
bin_dir="$(cd "$(dirname "$0")/src-tauri/binaries" && pwd)"
mkdir -p "$bin_dir"

case "$triple" in
  aarch64-apple-darwin)
    ff_zip="https://www.osxexperts.net/ffmpeg9arm.zip"
    fp_zip="https://www.osxexperts.net/ffprobe9arm.zip"
    ff_sha="591260c945d0eef150e3bf82b0ef988bd36a9cecc18ff05d6679617159f0a95e"
    fp_sha="e11c17e8200b3ee4c4c186d245e2b4053f01d56957336c1817fca0b997469106"
    ;;
  x86_64-apple-darwin)
    ff_zip="https://www.osxexperts.net/ffmpeg80intel.zip"
    fp_zip="https://www.osxexperts.net/ffprobe80intel.zip"
    ff_sha="df3f1e3facdc1ae0ad0bd898cdfb072fbc9641bf47b11f172844525a05db8d11"
    fp_sha="5228e651e2bd67bb55819b27f6138351587b16d2b87446007bf35b7cf930d891"
    ;;
  *)
    echo "fetch-ffmpeg.sh 不覆盖目标 ${triple}（Windows 走 CI PowerShell 步骤）" >&2
    exit 2
    ;;
esac

ff_out="$bin_dir/ffmpeg-$triple"
fp_out="$bin_dir/ffprobe-$triple"
# 幂等戳：记录签名后的实际哈希（ad-hoc codesign 会改文件哈希，不能直接对官方哈希做幂等）
ff_stamp="$bin_dir/ffmpeg-$triple.sha256"
fp_stamp="$bin_dir/ffprobe-$triple.sha256"

# 幂等：已就位且与本地戳哈希吻合则跳过（make desktop 每次都会调，避免重复下载几十 MB）
if [ -f "$ff_out" ] && [ -f "$fp_out" ] \
  && [ "$(cat "$ff_stamp" 2>/dev/null)" = "$(shasum -a 256 "$ff_out" | cut -d' ' -f1)" ] \
  && [ "$(cat "$fp_stamp" 2>/dev/null)" = "$(shasum -a 256 "$fp_out" | cut -d' ' -f1)" ]; then
  echo "FFmpeg 已就位且哈希吻合，跳过（${triple}）"
  exit 0
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
echo "下载 FFmpeg 静态构建（${triple}）..."
curl -sSfL --retry 3 -o "$tmp/ffmpeg.zip" "$ff_zip"
curl -sSfL --retry 3 -o "$tmp/ffprobe.zip" "$fp_zip"
unzip -oq "$tmp/ffmpeg.zip" -d "$tmp/ff"
unzip -oq "$tmp/ffprobe.zip" -d "$tmp/fp"

# osxexperts 公布的是解压后二进制文件的 SHA256
echo "$ff_sha  $tmp/ff/ffmpeg" | shasum -a 256 -c - >/dev/null \
  || { echo "ffmpeg 哈希不符：上游可能已原地更新，请重钉 fetch-ffmpeg.sh 中的哈希" >&2; exit 1; }
echo "$fp_sha  $tmp/fp/ffprobe" | shasum -a 256 -c - >/dev/null \
  || { echo "ffprobe 哈希不符：上游可能已原地更新，请重钉 fetch-ffmpeg.sh 中的哈希" >&2; exit 1; }

install -m 0755 "$tmp/ff/ffmpeg" "$ff_out"
install -m 0755 "$tmp/fp/ffprobe" "$fp_out"
# ad-hoc 签名：未签名的内嵌二进制在新版 macOS 上可能被 Gatekeeper 拒绝执行
# （分发渠道未做 notarization，ad-hoc 与 voxbox sidecar 现状一致）
if command -v codesign >/dev/null 2>&1; then
  codesign --force --sign - "$ff_out" "$fp_out" 2>/dev/null || true
fi
# 落幂等戳（签名后的哈希）；下次运行哈希吻合即跳过
shasum -a 256 "$ff_out" | cut -d' ' -f1 > "$ff_stamp"
shasum -a 256 "$fp_out" | cut -d' ' -f1 > "$fp_stamp"
echo "已放置 $(basename "$ff_out") / $(basename "$fp_out")"

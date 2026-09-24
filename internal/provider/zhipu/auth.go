package zhipu

import "errors"

// ErrNoCred 凭证未配置哨兵：CLI 退出码与 Web 业务码按此映射为 4（配置类问题，重试无意义），
// 与 qianwen/xiaomi.ErrNoCred 同款模式。工具层以 %w 包装并附「智谱 API Key」配置指引文案。
var ErrNoCred = errors.New("智谱 API Key 未配置")

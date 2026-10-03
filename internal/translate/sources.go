package translate

// 火山/免费源适配:与 AI 源同签名的批翻译函数;包级变量供测试缝替换。

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/freetranslate"
	"github.com/yann0917/voxbox/internal/provider/volcengine"
)

// volcCredOf 从配置取火山凭证(与 provider 注册同一装配口径)。
func volcCredOf(cfg *config.Config) volcengine.SpeechCred {
	return volcengine.SpeechCred{
		AppID:       cfg.Volc.Speech.AppID,
		AccessToken: cfg.Volc.Speech.AccessToken,
		APIKey:      cfg.Volc.Speech.APIKey,
	}
}

// volcTranslate 火山机器翻译批翻译:TextList 单次 ≤16 条,引擎已按 16 切批,
// 这里对超长批次兜底再切;凭证缺失透出 ErrNoCred 包装错误(failErr 可映射业务码 4)。
var volcTranslate = func(ctx context.Context, cfg *config.Config, lines []string, srcLang, tgtLang string) ([]string, error) {
	if err := volcCredOf(cfg).Validate(); err != nil {
		return nil, err
	}
	client := volcengine.NewMTClient(volcCredOf(cfg))
	out := make([]string, 0, len(lines))
	for start := 0; start < len(lines); start += batchSizeVolc {
		end := start + batchSizeVolc
		if end > len(lines) {
			end = len(lines)
		}
		items, err := client.Translate(ctx, volcengine.MTTranslateReq{
			SourceLanguage: srcLang,
			TargetLanguage: tgtLang,
			TextList:       lines[start:end],
		})
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			out = append(out, it.Translation)
		}
	}
	if len(out) != len(lines) {
		return nil, fmt.Errorf("火山译文数 %d 与原文数 %d 不符", len(out), len(lines))
	}
	return out, nil
}

// freeTranslate 免费链批翻译:google 免费 2 并发 + 150ms 步进(对公共接口礼貌),
// 逐行走回退链;单条终败即整批报错。
var freeTranslate = func(ctx context.Context, cfg *config.Config, lines []string, srcLang, tgtLang string) ([]string, error) {
	chain := freetranslate.Chain(cfg.Translate.DeepLXURL)
	out := make([]string, len(lines))
	const workers = 2
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		next     = 0
		firstErr error
	)
	for w := 0; w < workers && w < len(lines); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				// next 与 firstErr 同锁守护:取号、终判、写结果全程持锁
				mu.Lock()
				i := next
				next++
				hasErr := firstErr != nil
				mu.Unlock()
				if i >= len(lines) || hasErr {
					return
				}
				tr, err := chain(ctx, lines[i], srcLang, tgtLang)
				mu.Lock()
				if err != nil {
					if firstErr == nil {
						firstErr = fmt.Errorf("第 %d 条: %w", i+1, err)
					}
				} else {
					out[i] = tr
				}
				mu.Unlock()
				time.Sleep(150 * time.Millisecond)
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if strings.TrimSpace(strings.Join(out, "")) == "" {
		return nil, fmt.Errorf("免费翻译未返回任何译文")
	}
	return out, nil
}

// freeTranslateProd 生产实现存档:测试直接替换 freeTranslate 后据此还原
// (engine_test.go 的 defer 还原模式依赖此值)。
var freeTranslateProd = freeTranslate

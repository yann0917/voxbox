package store

import (
	"fmt"
	"testing"
)

// 翻译术语表 repo 测试:upsert 新增/修订、按最近使用排序、语言隔离、超限修剪。

func TestGlossaryUpsertAndLoad(t *testing.T) {
	db := openTest(t)
	if err := db.UpsertGlossary("en", []GlossaryPair{{Src: "张三", Dst: "Zhang San"}}); err != nil {
		t.Fatal(err)
	}
	pairs, err := db.LoadGlossary("en", 0)
	if err != nil || len(pairs) != 1 || pairs[0].Src != "张三" || pairs[0].Dst != "Zhang San" {
		t.Fatalf("pairs=%+v err=%v", pairs, err)
	}
	// 修订:同词覆盖译法,不新增行
	if err := db.UpsertGlossary("en", []GlossaryPair{{Src: "张三", Dst: "Zhang San II"}}); err != nil {
		t.Fatal(err)
	}
	pairs, _ = db.LoadGlossary("en", 0)
	if len(pairs) != 1 || pairs[0].Dst != "Zhang San II" {
		t.Fatalf("修订应覆盖: %+v", pairs)
	}
	// 语言隔离
	_ = db.UpsertGlossary("ja", []GlossaryPair{{Src: "张三", Dst: "張三さん"}})
	pairs, _ = db.LoadGlossary("en", 0)
	if len(pairs) != 1 {
		t.Fatalf("语言应隔离: %+v", pairs)
	}
}

func TestGlossaryLoadRecency(t *testing.T) {
	db := openTest(t)
	_ = db.UpsertGlossary("en", []GlossaryPair{{Src: "先学", Dst: "first"}})
	_ = db.UpsertGlossary("en", []GlossaryPair{{Src: "后学", Dst: "second"}})
	pairs, err := db.LoadGlossary("en", 1)
	if err != nil || len(pairs) != 1 || pairs[0].Src != "后学" {
		t.Fatalf("limit 加载应取最近使用: %+v err=%v", pairs, err)
	}
	pairs, _ = db.LoadGlossary("en", 0)
	if len(pairs) != 2 || pairs[0].Src != "后学" || pairs[1].Src != "先学" {
		t.Fatalf("应按最近使用在前: %+v", pairs)
	}
}

func TestGlossaryPrune(t *testing.T) {
	db := openTest(t)
	rows := make([]GlossaryPair, 0, glossaryKeepMax+2)
	for i := 0; i < glossaryKeepMax+2; i++ {
		rows = append(rows, GlossaryPair{Src: fmt.Sprintf("词%04d", i), Dst: fmt.Sprintf("t%d", i)})
	}
	if err := db.UpsertGlossary("en", rows); err != nil {
		t.Fatal(err)
	}
	pairs, _ := db.LoadGlossary("en", 0)
	if len(pairs) != glossaryKeepMax {
		t.Fatalf("超限应修剪到 %d, got %d", glossaryKeepMax, len(pairs))
	}
	// 修剪按最近使用:最早写入的最旧两条(词0000/词0001)应被淘汰
	for _, p := range pairs {
		if p.Src == "词0000" || p.Src == "词0001" {
			t.Fatalf("最旧条目应被修剪: %q 仍在", p.Src)
		}
	}
	// 再学一条新词:总量守恒
	if err := db.UpsertGlossary("en", []GlossaryPair{{Src: "新词", Dst: "new"}}); err != nil {
		t.Fatal(err)
	}
	pairs, _ = db.LoadGlossary("en", 0)
	if len(pairs) != glossaryKeepMax {
		t.Fatalf("修剪后总量应守恒, got %d", len(pairs))
	}
}

package store

import (
	"testing"
)

// 翻译术语表 repo 测试:AI upsert 新增/修订、按最近使用排序、语言隔离、人工增删改查。

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

func TestGlossaryManualCRUD(t *testing.T) {
	db := openTest(t)
	// AI 学习先沉淀两条
	if err := db.UpsertGlossary("en", []GlossaryPair{{Src: "张三", Dst: "Zhang San"}, {Src: "云帆号", Dst: "Yunfan"}}); err != nil {
		t.Fatal(err)
	}
	// 人工新增:与 AI 沉淀同表共存
	row, err := db.CreateGlossaryTerm("en", "李四", "Li Si")
	if err != nil || row.ID == 0 {
		t.Fatalf("人工新增失败: %+v err=%v", row, err)
	}
	// 人工新增:同语言同原文 → ErrGlossaryExists(不静默覆盖)
	if _, err := db.CreateGlossaryTerm("en", "张三", "x"); err != ErrGlossaryExists {
		t.Fatalf("撞唯一约束应报 ErrGlossaryExists, err=%v", err)
	}
	// 人工修改:改译法并刷新时间
	if _, err := db.UpdateGlossaryTerm(row.ID, "en", "李四", "Li Si II"); err != nil {
		t.Fatal(err)
	}
	pairs, _ := db.LoadGlossary("en", 0)
	if pairs[0].Src != "李四" || pairs[0].Dst != "Li Si II" {
		t.Fatalf("修改后应覆盖且置顶: %+v", pairs)
	}
	// 人工修改:改出的 (lang,src) 与他人行冲突 → ErrGlossaryExists
	if _, err := db.UpdateGlossaryTerm(row.ID, "en", "张三", "x"); err != ErrGlossaryExists {
		t.Fatalf("改词撞行应报 ErrGlossaryExists, err=%v", err)
	}
	// 人工修改:不存在 id → ErrNotFound
	if _, err := db.UpdateGlossaryTerm(9999, "en", "王五", "Wang Wu"); err != ErrNotFound {
		t.Fatalf("不存在 id 应报 ErrNotFound, err=%v", err)
	}
	// 列表:lang 过滤与全量
	rows, err := db.ListGlossaryRows("en")
	if err != nil || len(rows) != 3 {
		t.Fatalf("en 应有 3 条: %v err=%v", rows, err)
	}
	all, _ := db.ListGlossaryRows("")
	if len(all) != 3 {
		t.Fatalf("全部语言应 3 条: %d", len(all))
	}
	if _, err := db.ListGlossaryRows("ja"); err != nil || len(all) != 3 {
		t.Fatalf("ja 过滤应为空但不出错: err=%v", err)
	}
	// 人工删除:后再改/删报 ErrNotFound
	if err := db.DeleteGlossaryTerm(row.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteGlossaryTerm(row.ID); err != ErrNotFound {
		t.Fatalf("重复删除应报 ErrNotFound, err=%v", err)
	}
	pairs, _ = db.LoadGlossary("en", 0)
	if len(pairs) != 2 {
		t.Fatalf("删除后应剩 2 条: %+v", pairs)
	}
}

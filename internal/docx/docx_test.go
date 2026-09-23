package docx

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestBuildAndValidate(t *testing.T) {
	d := New()
	d.Heading(1, "会议信息")
	d.Paragraph("时间：2026-09-15\n时长：12:00")
	d.Table(
		[]string{"#", "待办", "执行人"},
		[][]string{
			{"1", "确认预算<上限>", "张三"},
			{"1", "缺列补空", "李四", "多余列应截断"},
		},
	)
	b, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.ValidateXML(); err != nil {
		t.Fatalf("XML 良构校验失败: %v", err)
	}

	// zip 结构完整
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, want := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml", "word/styles.xml"} {
		if !names[want] {
			t.Errorf("缺少 %s", want)
		}
	}
}

func TestEscapeAndCells(t *testing.T) {
	d := New()
	d.Paragraph("a<b>&\"c\"")
	d.Table([]string{"h1", "h2", "h3"}, [][]string{{"只", "两", "列", "超出"}})
	b, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.ValidateXML(); err != nil {
		t.Fatalf("XML 良构校验失败: %v", err)
	}
	// document.xml 中特殊字符必须被转义
	zr, _ := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		buf := new(bytes.Buffer)
		rc, _ := f.Open()
		_, _ = buf.ReadFrom(rc)
		rc.Close()
		doc := buf.String()
		if strings.Contains(doc, "a<b>") {
			t.Error("特殊字符未转义")
		}
		if strings.Contains(doc, "超出") {
			t.Error("超出表头列数的单元格应被截断")
		}
		if !strings.Contains(doc, "只") || !strings.Contains(doc, "两") {
			t.Error("前两列应保留")
		}
	}
}

// Package docx 生成极简 OOXML Word 文档（.docx = zip + XML，规范 ECMA-376）。
// 只覆盖 voxbox 需要的子集：标题/段落/表格（带边框表头），零第三方依赖。
// 生成结构经 Word/WPS/macOS textutil 实测可打开。
package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

type Doc struct {
	body strings.Builder
}

func New() *Doc { return &Doc{} }

func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// Heading 标题段落（level 1/2 → Heading1/Heading2 样式，见 styles.xml）。
func (d *Doc) Heading(level int, text string) {
	fmt.Fprintf(&d.body,
		`<w:p><w:pPr><w:pStyle w:val="Heading%d"/></w:pPr><w:r><w:t xml:space="preserve">%s</w:t></w:r></w:p>`,
		level, esc(text))
}

// Paragraph 普通段落；text 中的 \n 转为换行 run。
func (d *Doc) Paragraph(text string) {
	lines := strings.Split(text, "\n")
	var runs strings.Builder
	for _, ln := range lines {
		runs.WriteString(`<w:r><w:t xml:space="preserve">` + esc(ln) + `</w:t></w:r>`)
		runs.WriteString(`<w:r><w:br/></w:r>`)
	}
	// 去掉末尾多余的 br，避免段尾空行
	out := strings.TrimSuffix(runs.String(), `<w:r><w:br/></w:r>`)
	d.body.WriteString(`<w:p>` + out + `</w:p>`)
}

// Table 带边框表格；headers 为表头行（加粗），rows 为数据行。
// 各行列数以 headers 为准：超长截断、不足补空，避免生成非法 OOXML。
func (d *Doc) Table(headers []string, rows [][]string) {
	var b strings.Builder
	b.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="0" w:type="auto"/><w:tblBorders>` +
		borders() + `</w:tblBorders></w:tblPr>`)
	// 表头行（tblHeader：跨页时重复）
	b.WriteString(`<w:tr><w:trPr><w:tblHeader/></w:trPr>`)
	for _, h := range headers {
		b.WriteString(`<w:tc><w:tcPr/><w:p><w:pPr><w:rPr><w:b/></w:rPr></w:pPr><w:r><w:rPr><w:b/></w:rPr><w:t xml:space="preserve">` + esc(h) + `</w:t></w:r></w:p></w:tc>`)
	}
	b.WriteString(`</w:tr>`)
	for _, row := range rows {
		b.WriteString(`<w:tr>`)
		for i := 0; i < len(headers); i++ {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			b.WriteString(`<w:tc><w:tcPr/><w:p><w:r><w:t xml:space="preserve">` + esc(cell) + `</w:t></w:r></w:p></w:tc>`)
		}
		b.WriteString(`</w:tr>`)
	}
	b.WriteString(`</w:tbl>`)
	// OOXML 要求表格后必须跟一个段落
	d.body.WriteString(b.String() + `<w:p/>`)
}

func borders() string {
	const edge = `<w:%s w:val="single" w:sz="4" w:space="0" w:color="999999"/>`
	return fmt.Sprintf(edge, "top") + fmt.Sprintf(edge, "left") +
		fmt.Sprintf(edge, "bottom") + fmt.Sprintf(edge, "right") +
		`<w:insideH w:val="single" w:sz="4" w:space="0" w:color="999999"/>` +
		`<w:insideV w:val="single" w:sz="4" w:space="0" w:color="999999"/>`
}

// Bytes 打包为 .docx 字节流。
func (d *Doc) Bytes() ([]byte, error) {
	document := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		d.body.String() +
		`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/></w:sectPr></w:body></w:document>`

	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/></Types>`

	rels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`

	docRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`

	styles := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:eastAsia="Microsoft YaHei"/><w:sz w:val="22"/></w:rPr></w:rPrDefault></w:docDefaults><w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:pPr><w:spacing w:after="160"/></w:pPr><w:rPr><w:b/><w:sz w:val="44"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:pPr><w:outlineLvl w:val="0"/><w:spacing w:before="240" w:after="120"/></w:pPr><w:rPr><w:b/><w:sz w:val="30"/><w:color w:val="B45309"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:pPr><w:outlineLvl w:val="1"/><w:spacing w:before="180" w:after="100"/></w:pPr><w:rPr><w:b/><w:sz w:val="26"/></w:rPr></w:style></w:styles>`

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"[Content_Types].xml":          contentTypes,
		"_rels/.rels":                  rels,
		"word/document.xml":            document,
		"word/_rels/document.xml.rels": docRels,
		"word/styles.xml":              styles,
	}
	// 固定写入顺序保证 zip 确定性
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml", "word/_rels/document.xml.rels", "word/styles.xml"} {
		w, err := zw.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(files[name])); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ValidateXML 供测试使用：校验 document.xml 是良构 XML。
func (d *Doc) ValidateXML() error {
	b, err := d.Bytes()
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return err
		}
		dec := xml.NewDecoder(rc)
		for {
			_, err = dec.Token()
			if err != nil {
				break
			}
		}
		rc.Close()
		if err != io.EOF {
			return fmt.Errorf("%s XML 不良构: %w", f.Name, err)
		}
	}
	return nil
}

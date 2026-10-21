// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// A minimal .xlsx (SpreadsheetML) writer: just enough OOXML for the project
// export (typed cells, bold/colored fonts, solid fills, date number formats,
// column widths, frozen panes, an autofilter and merged cells), written with
// archive/zip and encoding/xml so the export adds no dependency. Strings go in
// the shared-string table, which every reader (Excel, Google Sheets, Numbers,
// LibreOffice) handles. Formulas, charts and themes are out of scope.

// xlsxStyle is one cell format. Equal styles share a cellXfs entry.
type xlsxStyle struct {
	Bold   bool
	Color  string // font color "#rrggbb"; "" = default
	Fill   string // solid fill "#rrggbb"; "" = none
	NumFmt string // number format code, e.g. "yyyy-mm-dd"; "" = General
	Center bool   // center horizontally
}

type xlsxKind int

const (
	xBlank xlsxKind = iota // no value; written only when it carries a style
	xString
	xNumber
)

// xlsxCell is one typed cell. The zero value is an empty, unstyled cell.
type xlsxCell struct {
	kind  xlsxKind
	str   string
	num   float64
	style int // index from xlsxWorkbook.style; 0 = default
}

func xStr(s string, style int) xlsxCell     { return xlsxCell{kind: xString, str: s, style: style} }
func xNum(n float64, style int) xlsxCell    { return xlsxCell{kind: xNumber, num: n, style: style} }
func xFill(style int) xlsxCell              { return xlsxCell{kind: xBlank, style: style} }
func xDate(d time.Time, style int) xlsxCell { return xNum(excelSerial(d), style) }

// excelSerial converts a calendar date to an Excel serial day number (the
// 1900 date system: day 1 is 1900-01-01, counted from 1899-12-30 to absorb
// Excel's phantom 1900-02-29).
func excelSerial(d time.Time) float64 {
	epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	day := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	return float64(daysBetween(epoch, day))
}

// xlsxSheet is one worksheet: rows of cells from A1, plus layout.
type xlsxSheet struct {
	name       string
	rows       [][]xlsxCell
	colWidths  []float64 // per column from A, in characters; 0 = default
	freezeRows int       // rows kept visible at the top
	freezeCols int       // columns kept visible at the left
	autoFilter string    // range like "A1:Q20"; "" = none
	merges     []string  // ranges like "A3:F3"
}

// xlsxWorkbook collects sheets and the styles their cells use.
type xlsxWorkbook struct {
	sheets   []*xlsxSheet
	styles   []xlsxStyle
	styleIdx map[xlsxStyle]int
}

func newXLSXWorkbook() *xlsxWorkbook {
	return &xlsxWorkbook{styles: []xlsxStyle{{}}, styleIdx: map[xlsxStyle]int{{}: 0}}
}

// addSheet appends an empty sheet. Names must be unique, at most 31
// characters and free of []:*?/\ (callers use fixed names).
func (wb *xlsxWorkbook) addSheet(name string) *xlsxSheet {
	s := &xlsxSheet{name: name}
	wb.sheets = append(wb.sheets, s)
	return s
}

// style returns the style id for s, registering it on first use.
func (wb *xlsxWorkbook) style(s xlsxStyle) int {
	s.Color, s.Fill = strings.ToUpper(s.Color), strings.ToUpper(s.Fill)
	if i, ok := wb.styleIdx[s]; ok {
		return i
	}
	wb.styles = append(wb.styles, s)
	wb.styleIdx[s] = len(wb.styles) - 1
	return len(wb.styles) - 1
}

// addRow appends a row and returns its 1-based row number.
func (s *xlsxSheet) addRow(cells ...xlsxCell) int {
	s.rows = append(s.rows, cells)
	return len(s.rows)
}

// xlsxCol returns the column letters for a 0-based index (0 → A, 26 → AA).
func xlsxCol(i int) string {
	var b []byte
	for i++; i > 0; i = (i - 1) / 26 {
		b = append([]byte{byte('A' + (i-1)%26)}, b...)
	}
	return string(b)
}

// xlsxRef returns a cell reference for 0-based column and 1-based row.
func xlsxRef(col, row int) string {
	return xlsxCol(col) + strconv.Itoa(row)
}

// xmlEsc escapes text for element content and attribute values. Characters
// XML 1.0 cannot carry (most control characters) become U+FFFD.
func xmlEsc(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// argb converts "#rrggbb" to the ARGB form OOXML wants ("FFRRGGBB").
func argb(hex string) string {
	return "FF" + strings.TrimPrefix(hex, "#")
}

const (
	nsMain = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
	nsRel  = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	nsPkg  = "http://schemas.openxmlformats.org/package/2006/relationships"
	xmlHdr = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
)

// bytes writes the workbook as a .xlsx (zip) file.
func (wb *xlsxWorkbook) bytes() ([]byte, error) {
	// Shared strings, in first-use order.
	var sst []string
	sstIdx := map[string]int{}
	strRef := func(s string) int {
		if i, ok := sstIdx[s]; ok {
			return i
		}
		sst = append(sst, s)
		sstIdx[s] = len(sst) - 1
		return len(sst) - 1
	}
	sheetXML := make([]string, len(wb.sheets))
	refs := 0
	for i, s := range wb.sheets {
		sheetXML[i] = s.xml(i == 0, strRef, &refs)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	put := func(name, body string) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = f.Write([]byte(xmlHdr + body))
		return err
	}

	var ct, wbRels, sheetsEl, defNames strings.Builder
	ct.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
		`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>` +
		`<Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/>`)
	wbRels.WriteString(`<Relationships xmlns="` + nsPkg + `">`)
	for i, s := range wb.sheets {
		n := i + 1
		ct.WriteString(fmt.Sprintf(`<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, n))
		wbRels.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="%s/worksheet" Target="worksheets/sheet%d.xml"/>`, n, nsRel, n))
		sheetsEl.WriteString(fmt.Sprintf(`<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, xmlEsc(s.name), n, n))
		if s.autoFilter != "" {
			// Excel records an autofilter's range as this hidden name.
			defNames.WriteString(fmt.Sprintf(`<definedName name="_xlnm._FilterDatabase" localSheetId="%d" hidden="1">%s</definedName>`,
				i, xmlEsc("'"+strings.ReplaceAll(s.name, "'", "''")+"'!"+absRange(s.autoFilter))))
		}
	}
	ct.WriteString(`</Types>`)
	n := len(wb.sheets)
	wbRels.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="%s/styles" Target="styles.xml"/>`, n+1, nsRel))
	wbRels.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="%s/sharedStrings" Target="sharedStrings.xml"/>`, n+2, nsRel))
	wbRels.WriteString(`</Relationships>`)
	workbook := `<workbook xmlns="` + nsMain + `" xmlns:r="` + nsRel + `"><bookViews><workbookView activeTab="0"/></bookViews><sheets>` +
		sheetsEl.String() + `</sheets>`
	if defNames.Len() > 0 {
		workbook += `<definedNames>` + defNames.String() + `</definedNames>`
	}
	workbook += `</workbook>`

	var ss strings.Builder
	ss.WriteString(fmt.Sprintf(`<sst xmlns="%s" count="%d" uniqueCount="%d">`, nsMain, refs, len(sst)))
	for _, s := range sst {
		ss.WriteString(`<si><t xml:space="preserve">` + xmlEsc(s) + `</t></si>`)
	}
	ss.WriteString(`</sst>`)

	parts := []struct{ name, body string }{
		{"[Content_Types].xml", ct.String()},
		{"_rels/.rels", `<Relationships xmlns="` + nsPkg + `"><Relationship Id="rId1" Type="` + nsRel + `/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", workbook},
		{"xl/_rels/workbook.xml.rels", wbRels.String()},
		{"xl/styles.xml", wb.stylesXML()},
		{"xl/sharedStrings.xml", ss.String()},
	}
	for i := range wb.sheets {
		parts = append(parts, struct{ name, body string }{fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), sheetXML[i]})
	}
	for _, p := range parts {
		if err := put(p.name, p.body); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// absRange turns "A1:Q20" into "$A$1:$Q$20".
func absRange(r string) string {
	var out []string
	for _, ref := range strings.Split(r, ":") {
		i := strings.IndexAny(ref, "0123456789")
		if i <= 0 {
			out = append(out, ref)
			continue
		}
		out = append(out, "$"+ref[:i]+"$"+ref[i:])
	}
	return strings.Join(out, ":")
}

// stylesXML builds xl/styles.xml: one font, fill and number format per
// distinct need, and one cellXfs entry per registered style (index = style id).
func (wb *xlsxWorkbook) stylesXML() string {
	type fontKey struct {
		bold  bool
		color string
	}
	fonts := []fontKey{{}}
	fontIdx := map[fontKey]int{{}: 0}
	fills := []string{"", ""} // 0 none and 1 gray125 are reserved
	fillIdx := map[string]int{}
	var numFmts []string
	numFmtIdx := map[string]int{}

	var xfs strings.Builder
	for _, s := range wb.styles {
		fk := fontKey{s.Bold, s.Color}
		fi, ok := fontIdx[fk]
		if !ok {
			fonts = append(fonts, fk)
			fi = len(fonts) - 1
			fontIdx[fk] = fi
		}
		fl := 0
		if s.Fill != "" {
			if fl, ok = fillIdx[s.Fill]; !ok {
				fills = append(fills, s.Fill)
				fl = len(fills) - 1
				fillIdx[s.Fill] = fl
			}
		}
		nf := 0
		if s.NumFmt != "" {
			if nf, ok = numFmtIdx[s.NumFmt]; !ok {
				numFmts = append(numFmts, s.NumFmt)
				nf = 163 + len(numFmts) // custom formats start at 164
				numFmtIdx[s.NumFmt] = nf
			}
		}
		xfs.WriteString(fmt.Sprintf(`<xf numFmtId="%d" fontId="%d" fillId="%d" borderId="0" xfId="0"`, nf, fi, fl))
		if nf != 0 {
			xfs.WriteString(` applyNumberFormat="1"`)
		}
		if fi != 0 {
			xfs.WriteString(` applyFont="1"`)
		}
		if fl != 0 {
			xfs.WriteString(` applyFill="1"`)
		}
		if s.Center {
			xfs.WriteString(` applyAlignment="1"><alignment horizontal="center"/></xf>`)
		} else {
			xfs.WriteString(`/>`)
		}
	}

	var b strings.Builder
	b.WriteString(`<styleSheet xmlns="` + nsMain + `">`)
	if len(numFmts) > 0 {
		b.WriteString(fmt.Sprintf(`<numFmts count="%d">`, len(numFmts)))
		for i, f := range numFmts {
			b.WriteString(fmt.Sprintf(`<numFmt numFmtId="%d" formatCode="%s"/>`, 164+i, xmlEsc(f)))
		}
		b.WriteString(`</numFmts>`)
	}
	b.WriteString(fmt.Sprintf(`<fonts count="%d">`, len(fonts)))
	for _, f := range fonts {
		b.WriteString(`<font>`)
		if f.bold {
			b.WriteString(`<b/>`)
		}
		b.WriteString(`<sz val="11"/>`)
		if f.color != "" {
			b.WriteString(`<color rgb="` + argb(f.color) + `"/>`)
		}
		b.WriteString(`<name val="Calibri"/><family val="2"/></font>`)
	}
	b.WriteString(`</fonts>`)
	b.WriteString(fmt.Sprintf(`<fills count="%d"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill>`, len(fills)))
	for _, f := range fills[2:] {
		b.WriteString(`<fill><patternFill patternType="solid"><fgColor rgb="` + argb(f) + `"/><bgColor indexed="64"/></patternFill></fill>`)
	}
	b.WriteString(`</fills>`)
	b.WriteString(`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>`)
	b.WriteString(`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>`)
	b.WriteString(fmt.Sprintf(`<cellXfs count="%d">%s</cellXfs>`, len(wb.styles), xfs.String()))
	b.WriteString(`<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>`)
	b.WriteString(`</styleSheet>`)
	return b.String()
}

// xml builds xl/worksheets/sheetN.xml. strRef interns a shared string; refs
// counts string cells (the sst "count").
func (s *xlsxSheet) xml(selected bool, strRef func(string) int, refs *int) string {
	maxCol := 0
	for _, r := range s.rows {
		if len(r) > maxCol {
			maxCol = len(r)
		}
	}
	var b strings.Builder
	b.WriteString(`<worksheet xmlns="` + nsMain + `" xmlns:r="` + nsRel + `">`)
	if len(s.rows) > 0 && maxCol > 0 {
		b.WriteString(`<dimension ref="A1:` + xlsxRef(maxCol-1, len(s.rows)) + `"/>`)
	}
	b.WriteString(`<sheetViews><sheetView workbookViewId="0"`)
	if selected {
		b.WriteString(` tabSelected="1"`)
	}
	if s.freezeRows > 0 || s.freezeCols > 0 {
		pane := "bottomRight"
		switch {
		case s.freezeCols == 0:
			pane = "bottomLeft"
		case s.freezeRows == 0:
			pane = "topRight"
		}
		b.WriteString(`><pane`)
		if s.freezeCols > 0 {
			b.WriteString(fmt.Sprintf(` xSplit="%d"`, s.freezeCols))
		}
		if s.freezeRows > 0 {
			b.WriteString(fmt.Sprintf(` ySplit="%d"`, s.freezeRows))
		}
		b.WriteString(fmt.Sprintf(` topLeftCell="%s" activePane="%s" state="frozen"/><selection pane="%s"/></sheetView>`,
			xlsxRef(s.freezeCols, s.freezeRows+1), pane, pane))
	} else {
		b.WriteString(`/>`)
	}
	b.WriteString(`</sheetViews><sheetFormatPr defaultRowHeight="15"/>`)
	var cols strings.Builder
	for i, w := range s.colWidths {
		if w > 0 {
			cols.WriteString(fmt.Sprintf(`<col min="%d" max="%d" width="%s" customWidth="1"/>`, i+1, i+1, strconv.FormatFloat(w, 'f', -1, 64)))
		}
	}
	if cols.Len() > 0 {
		b.WriteString(`<cols>` + cols.String() + `</cols>`)
	}
	b.WriteString(`<sheetData>`)
	for ri, row := range s.rows {
		rn := ri + 1
		b.WriteString(fmt.Sprintf(`<row r="%d">`, rn))
		for ci, c := range row {
			ref := xlsxRef(ci, rn)
			st := ""
			if c.style != 0 {
				st = fmt.Sprintf(` s="%d"`, c.style)
			}
			switch c.kind {
			case xString:
				*refs++
				b.WriteString(fmt.Sprintf(`<c r="%s"%s t="s"><v>%d</v></c>`, ref, st, strRef(c.str)))
			case xNumber:
				b.WriteString(fmt.Sprintf(`<c r="%s"%s><v>%s</v></c>`, ref, st, strconv.FormatFloat(c.num, 'f', -1, 64)))
			default:
				if c.style != 0 {
					b.WriteString(fmt.Sprintf(`<c r="%s"%s/>`, ref, st))
				}
			}
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData>`)
	if s.autoFilter != "" {
		b.WriteString(`<autoFilter ref="` + s.autoFilter + `"/>`)
	}
	if len(s.merges) > 0 {
		b.WriteString(fmt.Sprintf(`<mergeCells count="%d">`, len(s.merges)))
		for _, m := range s.merges {
			b.WriteString(`<mergeCell ref="` + m + `"/>`)
		}
		b.WriteString(`</mergeCells>`)
	}
	b.WriteString(`</worksheet>`)
	return b.String()
}

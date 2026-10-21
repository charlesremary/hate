// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pm

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
)

// xlsxRead is a parsed .xlsx for tests: every part's raw XML, and each
// sheet's cells by reference with shared strings resolved.
type xlsxRead struct {
	parts  map[string]string
	sheets []string // names, in workbook order
	cells  []map[string]xlsxReadCell
	rows   []int // highest row number per sheet
}

type xlsxReadCell struct {
	typ   string // "s" (shared string) or "" (number / blank)
	value string // resolved string, or the raw number
	style int
}

// readXLSX unzips b, checks every XML part is well-formed, and parses the
// workbook, shared strings and sheets.
func readXLSX(t *testing.T, b []byte) *xlsxRead {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	out := &xlsxRead{parts: map[string]string{}}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		out.parts[f.Name] = string(data)
		dec := xml.NewDecoder(bytes.NewReader(data))
		for {
			if _, err := dec.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("%s is not well-formed XML: %v", f.Name, err)
			}
		}
	}
	for _, p := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/styles.xml", "xl/sharedStrings.xml"} {
		if _, ok := out.parts[p]; !ok {
			t.Fatalf("missing part %s", p)
		}
	}

	var wbx struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal([]byte(out.parts["xl/workbook.xml"]), &wbx); err != nil {
		t.Fatal(err)
	}
	var sst struct {
		SI []struct {
			T string `xml:"t"`
		} `xml:"si"`
	}
	if err := xml.Unmarshal([]byte(out.parts["xl/sharedStrings.xml"]), &sst); err != nil {
		t.Fatal(err)
	}
	for i, s := range wbx.Sheets {
		out.sheets = append(out.sheets, s.Name)
		var ws struct {
			Rows []struct {
				R     int `xml:"r,attr"`
				Cells []struct {
					R string `xml:"r,attr"`
					T string `xml:"t,attr"`
					S int    `xml:"s,attr"`
					V string `xml:"v"`
				} `xml:"c"`
			} `xml:"sheetData>row"`
		}
		body, ok := out.parts["xl/worksheets/sheet"+strconv.Itoa(i+1)+".xml"]
		if !ok {
			t.Fatalf("missing sheet part %d", i+1)
		}
		if err := xml.Unmarshal([]byte(body), &ws); err != nil {
			t.Fatal(err)
		}
		cells := map[string]xlsxReadCell{}
		maxRow := 0
		for _, r := range ws.Rows {
			if r.R > maxRow {
				maxRow = r.R
			}
			for _, c := range r.Cells {
				v := c.V
				if c.T == "s" {
					n, err := strconv.Atoi(c.V)
					if err != nil || n >= len(sst.SI) {
						t.Fatalf("%s: bad shared-string index %q", c.R, c.V)
					}
					v = sst.SI[n].T
				}
				cells[c.R] = xlsxReadCell{typ: c.T, value: v, style: c.S}
			}
		}
		out.cells = append(out.cells, cells)
		out.rows = append(out.rows, maxRow)
	}
	return out
}

func TestXLSXCol(t *testing.T) {
	for i, want := range map[int]string{0: "A", 25: "Z", 26: "AA", 51: "AZ", 52: "BA", 701: "ZZ", 702: "AAA"} {
		if got := xlsxCol(i); got != want {
			t.Errorf("xlsxCol(%d) = %q, want %q", i, got, want)
		}
	}
}

func TestExcelSerial(t *testing.T) {
	// Excel: 1900-03-01 = 61, 2026-11-02 = 46328.
	if got := excelSerial(time.Date(1900, 3, 1, 0, 0, 0, 0, time.UTC)); got != 61 {
		t.Errorf("1900-03-01 = %v, want 61", got)
	}
	if got := excelSerial(time.Date(2026, 11, 2, 15, 4, 0, 0, time.UTC)); got != 46328 {
		t.Errorf("2026-11-02 = %v, want 46328", got)
	}
}

func TestXLSXWriter(t *testing.T) {
	wb := newXLSXWorkbook()
	bold := wb.style(xlsxStyle{Bold: true})
	if again := wb.style(xlsxStyle{Bold: true}); again != bold {
		t.Errorf("equal styles not shared: %d vs %d", bold, again)
	}
	green := wb.style(xlsxStyle{Fill: "#22c55e"})
	date := wb.style(xlsxStyle{NumFmt: "yyyy-mm-dd"})
	sh := wb.addSheet("Data")
	title := `Fix <tag> & "quotes" 🚀`
	sh.addRow(xStr("Name", bold), xStr("N", bold), xStr("When", bold))
	sh.addRow(xStr(title, 0), xNum(1.5, 0), xDate(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), date), xFill(green))
	sh.addRow(xStr("ctl\x01char", 0))
	sh.colWidths = []float64{60, 0, 12}
	sh.freezeRows, sh.freezeCols = 1, 2
	sh.autoFilter = "A1:C3"
	sh.merges = []string{"A3:C3"}
	b, err := wb.bytes()
	if err != nil {
		t.Fatal(err)
	}
	x := readXLSX(t, b)
	if len(x.sheets) != 1 || x.sheets[0] != "Data" {
		t.Fatalf("sheets = %v", x.sheets)
	}
	c := x.cells[0]
	if c["A2"].value != title || c["A2"].typ != "s" {
		t.Errorf("A2 = %+v, want the title as a string", c["A2"])
	}
	if c["B2"].typ != "" || c["B2"].value != "1.5" {
		t.Errorf("B2 = %+v, want number 1.5", c["B2"])
	}
	if c["C2"].value != "46328" || c["C2"].style != date {
		t.Errorf("C2 = %+v, want serial 46328 with the date style", c["C2"])
	}
	if c["D2"].style != green || c["D2"].value != "" {
		t.Errorf("D2 = %+v, want a blank green-filled cell", c["D2"])
	}
	if strings.Contains(c["A3"].value, "\x01") {
		t.Error("control character not replaced")
	}
	ws := x.parts["xl/worksheets/sheet1.xml"]
	for _, want := range []string{
		`<pane xSplit="2" ySplit="1" topLeftCell="C2" activePane="bottomRight" state="frozen"/>`,
		`<col min="1" max="1" width="60" customWidth="1"/>`,
		`<autoFilter ref="A1:C3"/>`,
		`<mergeCell ref="A3:C3"/>`,
	} {
		if !strings.Contains(ws, want) {
			t.Errorf("sheet missing %s", want)
		}
	}
	if strings.Contains(ws, `<col min="2"`) {
		t.Error("a 0 width should keep the default column")
	}
	// autoFilter comes before mergeCells (schema order).
	if strings.Index(ws, "<autoFilter") > strings.Index(ws, "<mergeCells") {
		t.Error("autoFilter must precede mergeCells")
	}
	if !strings.Contains(x.parts["xl/workbook.xml"], `<definedName name="_xlnm._FilterDatabase" localSheetId="0" hidden="1">&#39;Data&#39;!$A$1:$C$3</definedName>`) {
		t.Error("missing _FilterDatabase defined name")
	}
	st := x.parts["xl/styles.xml"]
	for _, want := range []string{
		`<fgColor rgb="FF22C55E"/>`,
		`<numFmt numFmtId="164" formatCode="yyyy-mm-dd"/>`,
		`<b/>`,
		`<patternFill patternType="gray125"/>`,
	} {
		if !strings.Contains(st, want) {
			t.Errorf("styles.xml missing %s", want)
		}
	}
	var styles struct {
		Xfs []struct {
			NumFmt int `xml:"numFmtId,attr"`
			Fill   int `xml:"fillId,attr"`
		} `xml:"cellXfs>xf"`
	}
	if err := xml.Unmarshal([]byte(st), &styles); err != nil {
		t.Fatal(err)
	}
	if len(styles.Xfs) != len(wb.styles) {
		t.Fatalf("cellXfs = %d, want %d", len(styles.Xfs), len(wb.styles))
	}
	if styles.Xfs[green].Fill != 2 || styles.Xfs[date].NumFmt != 164 {
		t.Errorf("xf mapping wrong: green fill %d, date numFmt %d", styles.Xfs[green].Fill, styles.Xfs[date].NumFmt)
	}
}

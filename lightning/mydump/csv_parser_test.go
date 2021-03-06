package mydump_test

import (
	"context"
	"encoding/csv"
	"io"
	"os"
	"path/filepath"
	"strings"

	. "github.com/pingcap/check"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/types"
	"go.uber.org/zap"

	"github.com/pingcap/tidb-lightning/lightning/config"
	"github.com/pingcap/tidb-lightning/lightning/log"
	"github.com/pingcap/tidb-lightning/lightning/mydump"
	"github.com/pingcap/tidb-lightning/lightning/worker"
)

var _ = Suite(&testMydumpCSVParserSuite{})

type testMydumpCSVParserSuite struct {
	ioWorkers *worker.Pool
}

func (s *testMydumpCSVParserSuite) SetUpSuite(c *C) {
	s.ioWorkers = worker.NewPool(context.Background(), 5, "test_csv")
}
func (s *testMydumpCSVParserSuite) TearDownSuite(c *C) {}

type assertPosEq struct {
	*CheckerInfo
}

var posEq = &assertPosEq{
	&CheckerInfo{Name: "posEq", Params: []string{"parser", "pos", "rowID"}},
}

func (checker *assertPosEq) Check(params []interface{}, names []string) (result bool, error string) {
	parser := params[0].(mydump.Parser)
	pos, rowID := parser.Pos()
	expectedPos := int64(params[1].(int))
	expectedRowID := int64(params[2].(int))
	return pos == expectedPos && rowID == expectedRowID, ""
}

var nullDatum types.Datum

type testCase struct {
	input    string
	expected [][]types.Datum
}

func (s *testMydumpCSVParserSuite) runTestCases(c *C, cfg *config.CSVConfig, blockBufSize int64, cases []testCase) {
	for _, tc := range cases {
		parser := mydump.NewCSVParser(cfg, mydump.NewStringReader(tc.input), blockBufSize, s.ioWorkers, false)
		for i, row := range tc.expected {
			comment := Commentf("input = %q, row = %d", tc.input, i+1)
			e := parser.ReadRow()
			c.Assert(e, IsNil, Commentf("input = %q, row = %d, error = %s", tc.input, i+1, errors.ErrorStack(e)))
			c.Assert(parser.LastRow(), DeepEquals, mydump.Row{RowID: int64(i) + 1, Row: row}, comment)
		}
		c.Assert(errors.Cause(parser.ReadRow()), Equals, io.EOF, Commentf("input = %q", tc.input))
	}
}

func (s *testMydumpCSVParserSuite) runFailingTestCases(c *C, cfg *config.CSVConfig, blockBufSize int64, cases []string) {
	for _, tc := range cases {
		parser := mydump.NewCSVParser(cfg, mydump.NewStringReader(tc), blockBufSize, s.ioWorkers, false)
		e := parser.ReadRow()
		c.Assert(e, ErrorMatches, "syntax error.*", Commentf("input = %q / %s", tc, errors.ErrorStack(e)))
	}
}

func tpchDatums() [][]types.Datum {
	datums := make([][]types.Datum, 0, 3)
	datums = append(datums, []types.Datum{
		types.NewStringDatum("1"),
		types.NewStringDatum("goldenrod lavender spring chocolate lace"),
		types.NewStringDatum("Manufacturer#1"),
		types.NewStringDatum("Brand#13"),
		types.NewStringDatum("PROMO BURNISHED COPPER"),
		types.NewStringDatum("7"),
		types.NewStringDatum("JUMBO PKG"),
		types.NewStringDatum("901.00"),
		types.NewStringDatum("ly. slyly ironi"),
	})
	datums = append(datums, []types.Datum{
		types.NewStringDatum("2"),
		types.NewStringDatum("blush thistle blue yellow saddle"),
		types.NewStringDatum("Manufacturer#1"),
		types.NewStringDatum("Brand#13"),
		types.NewStringDatum("LARGE BRUSHED BRASS"),
		types.NewStringDatum("1"),
		types.NewStringDatum("LG CASE"),
		types.NewStringDatum("902.00"),
		types.NewStringDatum("lar accounts amo"),
	})
	datums = append(datums, []types.Datum{
		types.NewStringDatum("3"),
		types.NewStringDatum("spring green yellow purple cornsilk"),
		types.NewStringDatum("Manufacturer#4"),
		types.NewStringDatum("Brand#42"),
		types.NewStringDatum("STANDARD POLISHED BRASS"),
		types.NewStringDatum("21"),
		types.NewStringDatum("WRAP CASE"),
		types.NewStringDatum("903.00"),
		types.NewStringDatum("egular deposits hag"),
	})

	return datums
}

func datumsToString(datums [][]types.Datum, delimitor string, quote string, lastSep bool) string {
	var b strings.Builder
	for _, ds := range datums {
		for i, d := range ds {
			b.WriteString(quote)
			b.WriteString(d.GetString())
			b.WriteString(quote)
			if lastSep || i < len(ds)-1 {
				b.WriteString(delimitor)
			}
		}
		b.WriteString("\r\n")
	}
	return b.String()
}

func (s *testMydumpCSVParserSuite) TestTPCH(c *C) {
	datums := tpchDatums()
	input := datumsToString(datums, "|", "", true)
	reader := mydump.NewStringReader(input)

	cfg := config.CSVConfig{
		Separator:   "|",
		Delimiter:   "",
		TrimLastSep: true,
	}

	parser := mydump.NewCSVParser(&cfg, reader, int64(config.ReadBlockSize), s.ioWorkers, false)

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 1,
		Row:   datums[0],
	})
	c.Assert(parser, posEq, 126, 1)

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 2,
		Row:   datums[1],
	})
	c.Assert(parser, posEq, 241, 2)

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 3,
		Row:   datums[2],
	})
	c.Assert(parser, posEq, 369, 3)

	c.Assert(errors.Cause(parser.ReadRow()), Equals, io.EOF)
}

func (s *testMydumpCSVParserSuite) TestTPCHMultiBytes(c *C) {
	datums := tpchDatums()
	deliAndSeps := [][2]string{
		{",", ""},
		{"\000", ""},
		{"，", ""},
		{"🤔", ""},
		{"，", "。"},
		{"||", ""},
		{"|+|", ""},
		{"##", ""},
		{"，", "'"},
		{"，", `"`},
		{"🤔", `''`},
		{"🤔", `"'`},
		{"🤔", `"'`},
		{"🤔", "🌚"}, // this two emoji have same prefix bytes
		{"##", "#-"},
		{"\\s", "\\q"},
	}
	for _, SepAndQuote := range deliAndSeps {
		inputStr := datumsToString(datums, SepAndQuote[0], SepAndQuote[1], false)
		cfg := config.CSVConfig{
			Separator:   SepAndQuote[0],
			Delimiter:   SepAndQuote[1],
			TrimLastSep: false,
		}

		reader := mydump.NewStringReader(inputStr)
		parser := mydump.NewCSVParser(&cfg, reader, int64(config.ReadBlockSize), s.ioWorkers, false)
		c.Assert(parser.ReadRow(), IsNil)
		c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
			RowID: 1,
			Row:   datums[0],
		})

		c.Assert(parser, posEq, 117+8*len(SepAndQuote[0])+18*len(SepAndQuote[1]), 1)

		c.Assert(parser.ReadRow(), IsNil)
		c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
			RowID: 2,
			Row:   datums[1],
		})
		c.Assert(parser, posEq, 223+8*2*len(SepAndQuote[0])+18*2*len(SepAndQuote[1]), 2)

		c.Assert(parser.ReadRow(), IsNil)
		c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
			RowID: 3,
			Row:   datums[2],
		})
		c.Assert(parser, posEq, 342+8*3*len(SepAndQuote[0])+18*3*len(SepAndQuote[1]), 3)

		c.Assert(errors.Cause(parser.ReadRow()), Equals, io.EOF)
	}
}

func (s *testMydumpCSVParserSuite) TestRFC4180(c *C) {
	cfg := config.CSVConfig{
		Separator: ",",
		Delimiter: `"`,
	}

	// example 1, trailing new lines

	parser := mydump.NewCSVParser(&cfg, mydump.NewStringReader("aaa,bbb,ccc\nzzz,yyy,xxx\n"), int64(config.ReadBlockSize), s.ioWorkers, false)

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 1,
		Row: []types.Datum{
			types.NewStringDatum("aaa"),
			types.NewStringDatum("bbb"),
			types.NewStringDatum("ccc"),
		},
	})
	c.Assert(parser, posEq, 12, 1)

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 2,
		Row: []types.Datum{
			types.NewStringDatum("zzz"),
			types.NewStringDatum("yyy"),
			types.NewStringDatum("xxx"),
		},
	})
	c.Assert(parser, posEq, 24, 2)

	c.Assert(errors.Cause(parser.ReadRow()), Equals, io.EOF)

	// example 2, no trailing new lines

	parser = mydump.NewCSVParser(&cfg, mydump.NewStringReader("aaa,bbb,ccc\nzzz,yyy,xxx"), int64(config.ReadBlockSize), s.ioWorkers, false)

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 1,
		Row: []types.Datum{
			types.NewStringDatum("aaa"),
			types.NewStringDatum("bbb"),
			types.NewStringDatum("ccc"),
		},
	})
	c.Assert(parser, posEq, 12, 1)

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 2,
		Row: []types.Datum{
			types.NewStringDatum("zzz"),
			types.NewStringDatum("yyy"),
			types.NewStringDatum("xxx"),
		},
	})
	c.Assert(parser, posEq, 23, 2)

	c.Assert(errors.Cause(parser.ReadRow()), Equals, io.EOF)

	// example 5, quoted fields

	parser = mydump.NewCSVParser(&cfg, mydump.NewStringReader(`"aaa","bbb","ccc"`+"\nzzz,yyy,xxx"), int64(config.ReadBlockSize), s.ioWorkers, false)

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 1,
		Row: []types.Datum{
			types.NewStringDatum("aaa"),
			types.NewStringDatum("bbb"),
			types.NewStringDatum("ccc"),
		},
	})
	c.Assert(parser, posEq, 18, 1)

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 2,
		Row: []types.Datum{
			types.NewStringDatum("zzz"),
			types.NewStringDatum("yyy"),
			types.NewStringDatum("xxx"),
		},
	})
	c.Assert(parser, posEq, 29, 2)

	c.Assert(errors.Cause(parser.ReadRow()), Equals, io.EOF)

	// example 6, line breaks within fields

	parser = mydump.NewCSVParser(&cfg, mydump.NewStringReader(`"aaa","b
bb","ccc"
zzz,yyy,xxx`), int64(config.ReadBlockSize), s.ioWorkers, false)

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 1,
		Row: []types.Datum{
			types.NewStringDatum("aaa"),
			types.NewStringDatum("b\nbb"),
			types.NewStringDatum("ccc"),
		},
	})
	c.Assert(parser, posEq, 19, 1)

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 2,
		Row: []types.Datum{
			types.NewStringDatum("zzz"),
			types.NewStringDatum("yyy"),
			types.NewStringDatum("xxx"),
		},
	})
	c.Assert(parser, posEq, 30, 2)

	c.Assert(errors.Cause(parser.ReadRow()), Equals, io.EOF)

	// example 7, quote escaping

	parser = mydump.NewCSVParser(&cfg, mydump.NewStringReader(`"aaa","b""bb","ccc"`), int64(config.ReadBlockSize), s.ioWorkers, false)

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 1,
		Row: []types.Datum{
			types.NewStringDatum("aaa"),
			types.NewStringDatum("b\"bb"),
			types.NewStringDatum("ccc"),
		},
	})
	c.Assert(parser, posEq, 19, 1)

	c.Assert(errors.Cause(parser.ReadRow()), Equals, io.EOF)
}

func (s *testMydumpCSVParserSuite) TestMySQL(c *C) {
	cfg := config.CSVConfig{
		Separator:       ",",
		Delimiter:       `"`,
		BackslashEscape: true,
		NotNull:         false,
		Null:            `\N`,
	}

	parser := mydump.NewCSVParser(&cfg, mydump.NewStringReader(`"\"","\\","\?"
"\
",\N,\\N`), int64(config.ReadBlockSize), s.ioWorkers, false)

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 1,
		Row: []types.Datum{
			types.NewStringDatum(`"`),
			types.NewStringDatum(`\`),
			types.NewStringDatum("?"),
		},
	})
	c.Assert(parser, posEq, 15, 1)

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 2,
		Row: []types.Datum{
			types.NewStringDatum("\n"),
			nullDatum,
			types.NewStringDatum(`\N`),
		},
	})
	c.Assert(parser, posEq, 26, 2)

	c.Assert(errors.Cause(parser.ReadRow()), Equals, io.EOF)
}

func (s *testMydumpCSVParserSuite) TestSyntaxError(c *C) {
	cfg := config.CSVConfig{
		Separator:       ",",
		Delimiter:       `"`,
		BackslashEscape: true,
	}

	inputs := []string{
		`"???`,
		`\`,
		`"\`,
		`0"`,
		`0\`,
		"\"\v",
		`"""`,
		"\"\r",
		"\"\x01",
	}

	s.runFailingTestCases(c, &cfg, int64(config.ReadBlockSize), inputs)

	cfg.BackslashEscape = false
	s.runFailingTestCases(c, &cfg, int64(config.ReadBlockSize), []string{`"\`})
}

func (s *testMydumpCSVParserSuite) TestTSV(c *C) {
	cfg := config.CSVConfig{
		Separator:       "\t",
		Delimiter:       "",
		BackslashEscape: false,
		NotNull:         false,
		Null:            "",
		Header:          true,
	}

	parser := mydump.NewCSVParser(&cfg, mydump.NewStringReader(`a	b	c	d	e	f
0				foo	0000-00-00
0				foo	0000-00-00
0	abc	def	ghi	bar	1999-12-31`), int64(config.ReadBlockSize), s.ioWorkers, true)

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 1,
		Row: []types.Datum{
			types.NewStringDatum("0"),
			nullDatum,
			nullDatum,
			nullDatum,
			types.NewStringDatum("foo"),
			types.NewStringDatum("0000-00-00"),
		},
	})
	c.Assert(parser, posEq, 32, 1)
	c.Assert(parser.Columns(), DeepEquals, []string{"a", "b", "c", "d", "e", "f"})

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 2,
		Row: []types.Datum{
			types.NewStringDatum("0"),
			nullDatum,
			nullDatum,
			nullDatum,
			types.NewStringDatum("foo"),
			types.NewStringDatum("0000-00-00"),
		},
	})
	c.Assert(parser, posEq, 52, 2)

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 3,
		Row: []types.Datum{
			types.NewStringDatum("0"),
			types.NewStringDatum("abc"),
			types.NewStringDatum("def"),
			types.NewStringDatum("ghi"),
			types.NewStringDatum("bar"),
			types.NewStringDatum("1999-12-31"),
		},
	})
	c.Assert(parser, posEq, 80, 3)

	c.Assert(errors.Cause(parser.ReadRow()), Equals, io.EOF)
}

func (s *testMydumpCSVParserSuite) TestCsvWithWhiteSpaceLine(c *C) {
	cfg := config.CSVConfig{
		Separator: ",",
		Delimiter: `"`,
	}
	data := " \r\n\r\n0,,abc\r\n \r\n123,1999-12-31,test\r\n"
	parser := mydump.NewCSVParser(&cfg, mydump.NewStringReader(data), int64(config.ReadBlockSize), s.ioWorkers, false)
	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 1,
		Row: []types.Datum{
			types.NewStringDatum("0"),
			nullDatum,
			types.NewStringDatum("abc"),
		},
	})

	c.Assert(parser, posEq, 12, 1)
	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 2,
		Row: []types.Datum{
			types.NewStringDatum("123"),
			types.NewStringDatum("1999-12-31"),
			types.NewStringDatum("test"),
		},
	})
	c.Assert(parser.Close(), IsNil)

	cfg.Header = true
	data = " \r\na,b,c\r\n0,,abc\r\n"
	parser = mydump.NewCSVParser(&cfg, mydump.NewStringReader(data), int64(config.ReadBlockSize), s.ioWorkers, true)
	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.Columns(), DeepEquals, []string{"a", "b", "c"})
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 1,
		Row: []types.Datum{
			types.NewStringDatum("0"),
			nullDatum,
			types.NewStringDatum("abc"),
		},
	})

	c.Assert(parser, posEq, 17, 1)
	c.Assert(parser.Close(), IsNil)
}

func (s *testMydumpCSVParserSuite) TestEmpty(c *C) {
	cfg := config.CSVConfig{
		Separator: ",",
		Delimiter: `"`,
	}

	parser := mydump.NewCSVParser(&cfg, mydump.NewStringReader(""), int64(config.ReadBlockSize), s.ioWorkers, false)
	c.Assert(errors.Cause(parser.ReadRow()), Equals, io.EOF)

	// Try again with headers.

	cfg.Header = true

	parser = mydump.NewCSVParser(&cfg, mydump.NewStringReader(""), int64(config.ReadBlockSize), s.ioWorkers, true)
	c.Assert(errors.Cause(parser.ReadRow()), Equals, io.EOF)

	parser = mydump.NewCSVParser(&cfg, mydump.NewStringReader("h\n"), int64(config.ReadBlockSize), s.ioWorkers, true)
	c.Assert(errors.Cause(parser.ReadRow()), Equals, io.EOF)
}

func (s *testMydumpCSVParserSuite) TestCRLF(c *C) {
	cfg := config.CSVConfig{
		Separator: ",",
		Delimiter: `"`,
	}
	parser := mydump.NewCSVParser(&cfg, mydump.NewStringReader("a\rb\r\nc\n\n\n\nd"), int64(config.ReadBlockSize), s.ioWorkers, false)

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 1,
		Row:   []types.Datum{types.NewStringDatum("a")},
	})

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 2,
		Row:   []types.Datum{types.NewStringDatum("b")},
	})

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 3,
		Row:   []types.Datum{types.NewStringDatum("c")},
	})

	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 4,
		Row:   []types.Datum{types.NewStringDatum("d")},
	})

	c.Assert(errors.Cause(parser.ReadRow()), Equals, io.EOF)
}

func (s *testMydumpCSVParserSuite) TestQuotedSeparator(c *C) {
	cfg := config.CSVConfig{
		Separator: ",",
		Delimiter: `"`,
	}

	parser := mydump.NewCSVParser(&cfg, mydump.NewStringReader(`",",','`), int64(config.ReadBlockSize), s.ioWorkers, false)
	c.Assert(parser.ReadRow(), IsNil)
	c.Assert(parser.LastRow(), DeepEquals, mydump.Row{
		RowID: 1,
		Row: []types.Datum{
			types.NewStringDatum(","),
			types.NewStringDatum("'"),
			types.NewStringDatum("'"),
		},
	})

	c.Assert(errors.Cause(parser.ReadRow()), Equals, io.EOF)
}

func (s *testMydumpCSVParserSuite) TestConsecutiveFields(c *C) {
	// Note: the behavior of reading `"xxx"yyy` here is undefined in RFC 4180.
	// Python's CSV module returns `xxxyyy`.
	// Rust's CSV package returns `xxxyyy`.
	// Go's CSV package returns a parse error.
	// NPM's CSV package returns a parse error.
	// MySQL's LOAD DATA statement returns `"xxx"yyy` as-is.

	cfg := config.CSVConfig{
		Separator: ",",
		Delimiter: `"`,
	}

	testCases := []string{
		`"x"?`,
		"\"\"\x01",
		"\"\"\v",
	}

	s.runFailingTestCases(c, &cfg, int64(config.ReadBlockSize), testCases)
}

func (s *testMydumpCSVParserSuite) TestSpecialChars(c *C) {
	cfg := config.CSVConfig{Separator: ",", Delimiter: `"`}
	testCases := []testCase{
		{
			input:    "\x00",
			expected: [][]types.Datum{{types.NewStringDatum("\x00")}},
		},
		{
			input:    `0\`,
			expected: [][]types.Datum{{types.NewStringDatum(`0\`)}},
		},
		{
			input:    `\`,
			expected: [][]types.Datum{{types.NewStringDatum(`\`)}},
		},
		{
			input:    "0\v",
			expected: [][]types.Datum{{types.NewStringDatum("0\v")}},
		},
		{
			input:    "0\x00",
			expected: [][]types.Datum{{types.NewStringDatum("0\x00")}},
		},
		{
			input:    "\n\r",
			expected: [][]types.Datum{},
		},
		{
			input:    `"""",0`,
			expected: [][]types.Datum{{types.NewStringDatum(`"`), types.NewStringDatum(`0`)}},
		},
	}

	s.runTestCases(c, &cfg, int64(config.ReadBlockSize), testCases)
}

func (s *testMydumpCSVParserSuite) TestContinuation(c *C) {
	cfg := config.CSVConfig{
		Separator:       ",",
		Delimiter:       `"`,
		BackslashEscape: true,
		TrimLastSep:     true,
	}

	testCases := []testCase{
		{
			input: `"abcdef",\njklm,nop` + "\r\n" + `"""""","\n",a,`,
			expected: [][]types.Datum{
				{
					types.NewStringDatum("abcdef"),
					types.NewStringDatum("\njklm"),
					types.NewStringDatum("nop"),
				},
				{
					types.NewStringDatum(`""`),
					types.NewStringDatum("\n"),
					types.NewStringDatum("a"),
				},
			},
		},
		{
			input:    `"VzMXdTXsLbiIqTYQlwPSudocNPKVsAqXgnuvupXEzlxkaFpBtHNDyoVEydoEgdnhsygaNHLpMTdEkpkrkNdzVjCbSoXvUqwoVaca"`,
			expected: [][]types.Datum{{types.NewStringDatum("VzMXdTXsLbiIqTYQlwPSudocNPKVsAqXgnuvupXEzlxkaFpBtHNDyoVEydoEgdnhsygaNHLpMTdEkpkrkNdzVjCbSoXvUqwoVaca")}},
		},
	}

	s.runTestCases(c, &cfg, 1, testCases)
}

func (s *testMydumpCSVParserSuite) TestBackslashAsSep(c *C) {
	cfg := config.CSVConfig{
		Separator: `\`,
		Delimiter: `"`,
	}

	testCases := []testCase{
		{
			input:    `0\`,
			expected: [][]types.Datum{{types.NewStringDatum("0"), nullDatum}},
		},
		{
			input:    `\`,
			expected: [][]types.Datum{{nullDatum, nullDatum}},
		},
	}

	s.runTestCases(c, &cfg, 1, testCases)

	failingInputs := []string{
		`"\`,
	}
	s.runFailingTestCases(c, &cfg, 1, failingInputs)
}

func (s *testMydumpCSVParserSuite) TestBackslashAsDelim(c *C) {
	cfg := config.CSVConfig{
		Separator: ",",
		Delimiter: `\`,
	}

	testCases := []testCase{
		{
			input:    `\\`,
			expected: [][]types.Datum{{nullDatum}},
		},
	}
	s.runTestCases(c, &cfg, 1, testCases)

	failingInputs := []string{
		`"\`,
	}
	s.runFailingTestCases(c, &cfg, 1, failingInputs)
}

// errorReader implements the Reader interface which always returns an error.
type errorReader struct{}

func (*errorReader) Read(p []byte) (int, error) {
	return 0, errors.New("fake read error")
}

func (*errorReader) Seek(offset int64, whence int) (int64, error) {
	return 0, errors.New("fake seek error")
}

func (*errorReader) Close() error {
	return errors.New("fake close error")
}

func (s *testMydumpCSVParserSuite) TestReadError(c *C) {
	cfg := config.CSVConfig{
		Separator: ",",
		Delimiter: `"`,
	}

	parser := mydump.NewCSVParser(&cfg, &errorReader{}, int64(config.ReadBlockSize), s.ioWorkers, false)
	c.Assert(parser.ReadRow(), ErrorMatches, "fake read error")
}

// TestSyntaxErrorLog checks that a syntax error won't dump huge strings into the log.
func (s *testMydumpCSVParserSuite) TestSyntaxErrorLog(c *C) {
	cfg := config.CSVConfig{
		Separator: "\t",
		Delimiter: "'",
	}

	tc := mydump.NewStringReader("x'" + strings.Repeat("y", 50000))
	parser := mydump.NewCSVParser(&cfg, tc, 50000, s.ioWorkers, false)
	logger, buffer := log.MakeTestLogger()
	parser.SetLogger(logger)
	c.Assert(parser.ReadRow(), ErrorMatches, "syntax error.*")
	c.Assert(logger.Sync(), IsNil)

	c.Assert(
		buffer.Stripped(), Equals,
		`{"$lvl":"ERROR","$msg":"syntax error","pos":1,"content":"'`+strings.Repeat("y", 255)+`"}`,
	)
}

// Run `go test github.com/pingcap/tidb-lightning/lightning/mydump -check.b -check.bmem -test.v` to get benchmark result.
// Please ensure your temporary storage has (c.N / 2) KiB of free space.

type benchCSVParserSuite struct {
	csvPath   string
	ioWorkers *worker.Pool
}

var _ = Suite(&benchCSVParserSuite{})

func (s *benchCSVParserSuite) setupTest(c *C) {
	s.ioWorkers = worker.NewPool(context.Background(), 5, "bench_csv")

	dir := c.MkDir()
	s.csvPath = filepath.Join(dir, "input.csv")
	file, err := os.Create(s.csvPath)
	c.Assert(err, IsNil)
	defer func() {
		c.Assert(file.Close(), IsNil)
	}()
	for i := 0; i < c.N; i++ {
		_, err = file.WriteString("18,1,1,0.3650,GC,BARBARBAR,rw9AOV1AjoI1,50000.00,-10.00,10.00,1,1,djj3Q2XaIPoYVy1FuF,gc80Q2o82Au3C9xv,PYOolSxG3w,DI,265111111,7586538936787184,2020-02-26 20:06:00.193,OE,YCkSPBVqoJ2V5F8zWs87V5XzbaIY70aWCD4dgcB6bjUzCr5wOJCJ2TYH49J7yWyysbudJIxlTAEWSJahY7hswLtTsqyjEkrlsN8iDMAa9Poj29miJ08tnn2G8mL64IlyywvnRGbLbyGvWDdrOSF42RyUFTWVyqlDWc6Gr5wyMPYgvweKemzFDVD3kro5JsmBmJY08EK54nQoyfo2sScyb34zcM9GFo9ZQTwloINfPYQKXQm32m0XvU7jiNmYpFTFJQjdqA825SEvQqMMefG2WG4jVu9UPdhdUjRsFRd0Gw7YPKByOlcuY0eKxT7sAzMKXx2000RR6dqHNXe47oVYd\n")
		c.Assert(err, IsNil)
	}
	c.ResetTimer()
}

func (s *benchCSVParserSuite) BenchmarkReadRowUsingMydumpCSVParser(c *C) {
	s.setupTest(c)

	file, err := os.Open(s.csvPath)
	c.Assert(err, IsNil)
	defer func() {
		c.Assert(file.Close(), IsNil)
	}()

	cfg := config.CSVConfig{Separator: ","}
	parser := mydump.NewCSVParser(&cfg, file, 65536, s.ioWorkers, false)
	parser.SetLogger(log.Logger{Logger: zap.NewNop()})

	rowsCount := 0
	for {
		err := parser.ReadRow()
		if err == nil {
			parser.RecycleRow(parser.LastRow())
			rowsCount++
			continue
		}
		if errors.Cause(err) == io.EOF {
			break
		}
		c.Fatal(err)
	}
	c.Assert(rowsCount, Equals, c.N)
}

func (s *benchCSVParserSuite) BenchmarkReadRowUsingEncodingCSV(c *C) {
	s.setupTest(c)

	file, err := os.Open(s.csvPath)
	c.Assert(err, IsNil)
	defer func() {
		c.Assert(file.Close(), IsNil)
	}()

	csvParser := csv.NewReader(file)

	rowsCount := 0
	var datums []types.Datum
	for {
		records, err := csvParser.Read()
		if err == nil {
			// for fair comparison, we need to include the cost of conversion to Datum.
			for _, record := range records {
				datums = append(datums, types.NewStringDatum(record))
			}
			datums = datums[:0]
			rowsCount++
			continue
		}
		if errors.Cause(err) == io.EOF {
			break
		}
		c.Fatal(err)
	}
	c.Assert(rowsCount, Equals, c.N)
}

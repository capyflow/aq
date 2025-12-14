package toml

import (
	"math"
	"strings"
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestArrayOfTables(t *testing.T) {
	convey.Convey("array of tables", t, func() {
		src := `
[[products]]
name = "Hammer"
sku = 738594937

[[products]]
name = "Nails"
sku = 284758393
count = 100
`
		root, err := Parse(strings.NewReader(src))
		convey.So(err, convey.ShouldBeNil)
		n, ok := Get(root, "products")
		convey.So(ok, convey.ShouldBeTrue)
		arr := n.(*Array)
		convey.So(len(arr.Elems), convey.ShouldEqual, 2)
		first := arr.Elems[0].(*Table)
		convey.So(MustString(first.Items["name"]), convey.ShouldEqual, "Hammer")
	})
}

func TestInlineTable(t *testing.T) {
	convey.Convey("inline table", t, func() {
		src := `owner = { name = "Tom", dob = 1979-05-27T07:32:00Z }`
		root, err := Parse(strings.NewReader(src))
		convey.So(err, convey.ShouldBeNil)
		n, ok := Get(root, "owner")
		convey.So(ok, convey.ShouldBeTrue)
		tbl := n.(*Table)
		convey.So(MustString(tbl.Items["name"]), convey.ShouldEqual, "Tom")
	})
}

func TestMultilineBasicString(t *testing.T) {
	convey.Convey("multiline basic string", t, func() {
		src := `desc = """first
second
third"""`
		root, err := Parse(strings.NewReader(src))
		convey.So(err, convey.ShouldBeNil)
		n, ok := Get(root, "desc")
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(MustString(n), convey.ShouldEqual, "first\nsecond\nthird")
	})
}

func TestQuotedKeys(t *testing.T) {
	convey.Convey("quoted keys", t, func() {
		src := `"a.b" = 1
a.c = 2`
		root, err := Parse(strings.NewReader(src))
		convey.So(err, convey.ShouldBeNil)
		n, ok := Get(root, "a.b")
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(MustInt(n), convey.ShouldEqual, 1)
		n2, ok2 := Get(root, "a", "c")
		convey.So(ok2, convey.ShouldBeTrue)
		convey.So(MustInt(n2), convey.ShouldEqual, 2)
	})
}

func TestSpecialFloatsAndInts(t *testing.T) {
	convey.Convey("floats and ints with underscores and bases", t, func() {
		src := `
f1 = +inf
f2 = -inf
f3 = nan
i1 = 1_000
hex = 0xDEADBEEF
oct = 0o755
bin = 0b1010
`
		root, err := Parse(strings.NewReader(src))
		convey.So(err, convey.ShouldBeNil)
		f1, _ := Get(root, "f1")
		convey.So(f1.(*Value).V.(float64), convey.ShouldEqual, math.Inf(+1))
		f2, _ := Get(root, "f2")
		convey.So(f2.(*Value).V.(float64), convey.ShouldEqual, math.Inf(-1))
		i1, _ := Get(root, "i1")
		convey.So(MustInt(i1), convey.ShouldEqual, 1000)
		hex, _ := Get(root, "hex")
		convey.So(MustInt(hex), convey.ShouldEqual, 0xDEADBEEF)
		oct, _ := Get(root, "oct")
		convey.So(MustInt(oct), convey.ShouldEqual, 0755)
		bin, _ := Get(root, "bin")
		convey.So(MustInt(bin), convey.ShouldEqual, 10)
	})
}

func TestMultilineArrayAndTrailingComma(t *testing.T) {
	convey.Convey("multiline array with trailing comma", t, func() {
		src := `
ports = [
  8001,
  8002,
]
`
		root, err := Parse(strings.NewReader(src))
		convey.So(err, convey.ShouldBeNil)
		n, ok := GetUntyped(root, "ports")
		convey.So(ok, convey.ShouldBeTrue)
		arr := n.([]any)
		convey.So(len(arr), convey.ShouldEqual, 2)
		convey.So(arr[0], convey.ShouldEqual, int64(8001))
		convey.So(arr[1], convey.ShouldEqual, int64(8002))
		t.Logf("%v", n)
	})
}

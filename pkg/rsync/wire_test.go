package rsync

import (
	"bytes"
	"io"
	"slices"
	"testing"
)

type loopback struct {
	bytes.Buffer
}

func newTestConn(protocol int) (*conn, *loopback) {
	var buf loopback
	c := newConn(&buf)
	c.protocol = protocol
	return c, &buf
}

func TestVarintRoundTrip(t *testing.T) {
	values := []int32{0, 1, 0x7f, 0x80, 0xff, 0x3fff, 0x4000, 0x1fffff, 0x200000, 0x0fffffff, 0x10000000, 0x7fffffff, -1, -2, -0x80000000}
	for _, v := range values {
		c, _ := newTestConn(31)
		if err := c.writeVarint(v); err != nil {
			t.Fatal(err)
		}
		if err := c.flush(); err != nil {
			t.Fatal(err)
		}
		got, err := c.readVarint()
		if err != nil || got != v {
			t.Errorf("varint %d: got %d, %v", v, got, err)
		}
	}
	if got := appendVarint(nil, 0x80); !bytes.Equal(got, []byte{0x80, 0x80}) {
		t.Errorf("varint 0x80 encodes as % x", got)
	}
}

func TestVarlongRoundTrip(t *testing.T) {
	values := []int64{0, 1, 0xffffff, 0x1000000, 0x7fffffff, 0x80000000, 1 << 40, 1<<62 + 12345, -1}
	for _, min := range []int{3, 4} {
		for _, v := range values {
			c, _ := newTestConn(31)
			if err := c.writeVarlong(v, min); err != nil {
				t.Fatal(err)
			}
			if err := c.flush(); err != nil {
				t.Fatal(err)
			}
			got, err := c.readVarlong(min)
			if err != nil || got != v {
				t.Errorf("varlong(%d) %d: got %d, %v", min, v, got, err)
			}
		}
	}
}

func TestLongintRoundTrip(t *testing.T) {
	for _, v := range []int64{0, 0x7fffffff, 0x80000000, 1 << 40, -5} {
		c, _ := newTestConn(29)
		if err := c.writeLongint(v); err != nil {
			t.Fatal(err)
		}
		if err := c.flush(); err != nil {
			t.Fatal(err)
		}
		got, err := c.readLongint()
		if err != nil || got != v {
			t.Errorf("longint %d: got %d, %v", v, got, err)
		}
	}
}

func TestNdxRoundTrip(t *testing.T) {
	seq := []int32{0, 1, 2, 5, 300, 299, 40000, 70000, 3, ndxDone, ndxDelStats, ndxFlistEOF, 7, ndxDone}
	for _, proto := range []int{29, 30} {
		c, _ := newTestConn(proto)
		for _, v := range seq {
			if err := c.writeNdx(v); err != nil {
				t.Fatal(err)
			}
		}
		if err := c.flush(); err != nil {
			t.Fatal(err)
		}
		for _, v := range seq {
			got, err := c.readNdx()
			if err != nil || got != v {
				t.Fatalf("protocol %d: ndx %d: got %d, %v", proto, v, got, err)
			}
		}
	}
}

func TestMultiplexedMessages(t *testing.T) {
	c, buf := newTestConn(31)
	if err := c.w.setMultiplex(true); err != nil {
		t.Fatal(err)
	}
	_ = c.writeInt32(42)
	_ = c.writeMsg(msgInfo, []byte("hello\n"))
	_ = c.writeInt32(43)
	if err := c.flush(); err != nil {
		t.Fatal(err)
	}

	r, _ := newTestConn(31)
	r.r.r.Reset(bytes.NewReader(buf.Bytes()))
	r.r.multiplex = true
	var msgs []string
	r.r.onMsg = func(tag byte, data []byte) error {
		msgs = append(msgs, string(data))
		return nil
	}
	a, err := r.readInt32()
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.readInt32()
	if err != nil {
		t.Fatal(err)
	}
	if a != 42 || b != 43 || !slices.Equal(msgs, []string{"hello\n"}) {
		t.Fatalf("got %d %d %q", a, b, msgs)
	}
	if _, err := r.readByte(); err != io.ErrUnexpectedEOF {
		t.Fatalf("want EOF, got %v", err)
	}
}

func TestChecksum1(t *testing.T) {
	// Bytes above 0x7f count as negative, as in rsync's C code.
	if got := checksum1([]byte{0xff, 1, 2, 3, 4, 5}); got != 0x001d000e {
		t.Errorf("checksum1 = %#x", got)
	}
	if got := checksum1(nil); got != 0 {
		t.Errorf("checksum1(nil) = %#x", got)
	}
}

func TestSumSizes(t *testing.T) {
	tests := []struct {
		length int64
		proto  int
		want   sumHead
	}{
		{0, 31, sumHead{0, 700, 2, 0}},
		{1, 31, sumHead{1, 700, 2, 1}},
		{1_000_000, 31, sumHead{1000, 1000, 2, 0}},
		{100 << 20, 31, sumHead{10240, 10240, 3, 0}},
		{100<<20 + 1, 29, sumHead{10241, 10240, 3, 1}},
	}
	for _, tt := range tests {
		got, err := sumSizesSqroot(tt.length, tt.proto, 0, shortSumLength, 16)
		if err != nil || got != tt.want {
			t.Errorf("sumSizes(%d, %d) = %+v, %v; want %+v", tt.length, tt.proto, got, err, tt.want)
		}
	}
	got, _ := sumSizesSqroot(1_000_000, 31, 0, sumLength, 8)
	if got.s2length != 8 {
		t.Errorf("full checksum length capped at digest size: got %d", got.s2length)
	}
}

func TestFileNameOrder(t *testing.T) {
	mk := func(name string, dir bool) *fileEntry {
		f := newFileEntry(name)
		if name == "." {
			f.dirname, f.basename = "", "."
		}
		f.mode = sIFREG
		if dir {
			f.mode = sIFDIR
		}
		return f
	}
	entries := func() []*fileEntry {
		return []*fileEntry{
			mk("b.txt", false), mk("a/x", false), mk("a", true), mk(".", true),
			mk("a.b", false), mk("a-b", false), mk("a/y/z", false), mk("a/y", true), mk("a/w", false),
		}
	}
	names := func(fs []*fileEntry) []string {
		var out []string
		for _, f := range fs {
			out = append(out, f.name())
		}
		return out
	}

	modern := entries()
	sortFileList(modern, 31, false)
	want := []string{".", "a-b", "a.b", "b.txt", "a", "a/w", "a/x", "a/y", "a/y/z"}
	if got := names(modern); !slices.Equal(got, want) {
		t.Errorf("protocol 31 order:\n got %q\nwant %q", got, want)
	}

	old := entries()
	sortFileList(old, 28, false)
	want = []string{".", "a", "a-b", "a.b", "a/w", "a/x", "a/y", "a/y/z", "b.txt"}
	if got := names(old); !slices.Equal(got, want) {
		t.Errorf("protocol 28 order:\n got %q\nwant %q", got, want)
	}
}

func TestDuplicateNames(t *testing.T) {
	a := newFileEntry("x")
	a.mode = sIFREG
	b := newFileEntry("x")
	b.mode = sIFDIR
	c := newFileEntry("x")
	c.mode = sIFREG
	files := []*fileEntry{a, b, c}
	sortFileList(files, 31, true)
	var active []*fileEntry
	for _, f := range files {
		if !f.inactive {
			active = append(active, f)
		}
	}
	if len(active) != 1 || !active[0].isDir() {
		t.Fatalf("want only the directory kept, got %d entries", len(active))
	}
}

func TestWildmatch(t *testing.T) {
	tests := []struct {
		pattern, text string
		want          bool
	}{
		{"foo", "foo", true},
		{"foo", "bar", false},
		{"???", "foo", true},
		{"??", "foo", false},
		{"*", "foo", true},
		{"f*", "foo", true},
		{"*f", "foo", false},
		{"*ob*a*r*", "foobar", true},
		{"*ab", "aaaaaaabababab", true},
		{`foo\*`, "foo*", true},
		{`foo\*bar`, "foobar", false},
		{"*[al]?", "ball", true},
		{"[ten]", "ten", false},
		{"**[!te]", "ten", true},
		{"**[!ten]", "ten", false},
		{"t[a-g]n", "ten", true},
		{"t[!a-g]n", "ten", false},
		{"t[^a-g]n", "ton", true},
		{"a[]]b", "a]b", true},
		{"[[:digit:]]x", "7x", true},
		{"[[:alpha:]]x", "7x", false},
		{"[[:bogus:]]", "a", false},
		{"*", "a/b", false},
		{"**", "a/b", true},
		{"a/*", "a/b/c", false},
		{"a/**", "a/b/c", true},
		{"**/c", "a/b/c", true},
		{"a?b", "a/b", false},
		{"[a/]b", "/b", false},
	}
	for _, tt := range tests {
		if got := wildmatch(tt.pattern, tt.text); got != tt.want {
			t.Errorf("wildmatch(%q, %q) = %v, want %v", tt.pattern, tt.text, got, tt.want)
		}
	}
}

func TestFilterRules(t *testing.T) {
	var l filterList
	for _, rule := range []string{"+ keep.log", "- *.log", "- /build/", "- node_modules/", "- docs/*.tmp", "-! *.html"} {
		if err := l.parse(rule, false); err != nil {
			t.Fatalf("parse %q: %v", rule, err)
		}
	}
	tests := []struct {
		name  string
		isDir bool
		want  int
	}{
		{"keep.log", false, 1},
		{"sub/keep.log", false, 1},
		{"x.log", false, -1},
		{"sub/x.log", false, -1},
		{"build", true, -1},
		{"build", false, -1}, // "-! *.html" excludes anything not ending in .html
		{"sub/build", true, -1},
		{"src/node_modules", true, -1},
		{"docs/a.tmp", false, -1},
		{"index.html", false, 0},
	}
	for _, tt := range tests {
		if got := l.check(tt.name, tt.isDir); got != tt.want {
			t.Errorf("check(%q, dir=%v) = %d, want %d", tt.name, tt.isDir, got, tt.want)
		}
	}

	var old filterList
	_ = old.parse("*.o", true)
	_ = old.parse("+ keep.o", true)
	if old.check("x.o", false) != -1 {
		t.Error("protocol 28 bare pattern is an exclude")
	}
	_ = old.parse("!", true)
	if len(old.rules) != 0 {
		t.Error("! clears the list")
	}
}

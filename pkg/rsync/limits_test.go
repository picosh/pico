package rsync

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/picosh/pico/pkg/rsync/rsyncopts"
)

func newLimitTestSession(t *testing.T, protocol int) *session {
	t.Helper()
	c, _ := newTestConn(t, protocol)
	return &session{
		c: c, logger: slog.New(slog.DiscardHandler), filters: &filterList{otherSide: ruleReceiverSide},
		cs: checksums{xfer: csumMD5}, opts: options{numericIDs: true},
	}
}

func TestBlockSizeLimits(t *testing.T) {
	for _, size := range []int64{0, maxBlockSize, maxBlockSize + 1, 0x7fffffff, 1<<32 + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			args := []string{"--server", fmt.Sprintf("--block-size=%d", size), ".", "file"}
			pc, err := rsyncopts.ParseArguments(args)
			if err != nil {
				t.Fatal(err)
			}
			err = checkSupported(pc.Options)
			if size <= maxBlockSize {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil {
				t.Fatal("accepted excessive block size")
			}
			rw := struct {
				io.Reader
				io.Writer
			}{strings.NewReader(""), io.Discard}
			err = Serve(Config{}, rw, args)
			var exit *ExitError
			if !errors.As(err, &exit) || exit.Code != exitUnsupported || !strings.Contains(err.Error(), "--block-size") {
				t.Fatalf("Serve = %v; want block-size rejection before negotiation", err)
			}
		})
	}
	for protocol := minProtocol; protocol <= maxProtocol; protocol++ {
		c, _ := newTestConn(t, protocol)
		if err := c.writeSumHead(sumHead{count: 1, blength: maxBlockSize + 1, s2length: sumLength}); err != nil {
			t.Fatal(err)
		}
		if err := c.flush(); err != nil {
			t.Fatal(err)
		}
		if _, err := c.readSumHead(sumLength); err == nil || !strings.Contains(err.Error(), "block length") {
			t.Fatalf("protocol %d accepted excessive block length: %v", protocol, err)
		}
	}
}

func TestGeneratedChecksumLimits(t *testing.T) {
	if _, err := sumSizesSqroot(1, maxBlockSize+1, shortSumLength, sumLength); err == nil {
		t.Fatal("generated checksums with an excessive block size")
	}
	if _, err := sumSizesSqroot(maxChecksumCount+1, 1, shortSumLength, sumLength); err == nil {
		t.Fatal("generated excessive checksum count")
	}
	head, err := sumSizesSqroot(maxChecksumCount, 1, shortSumLength, sumLength)
	if err != nil || head.count != maxChecksumCount {
		t.Fatalf("checksum count at limit = %d, %v", head.count, err)
	}
	cs := &checksums{xfer: csumMD5}
	for _, head := range []sumHead{
		{count: maxChecksumCount + 1, blength: 1, s2length: sumLength},
		{count: 1, blength: 0x7fffffff, s2length: sumLength},
	} {
		if _, err := encodeSums(bytes.NewReader([]byte{1}), 1, head, cs); err == nil {
			t.Fatalf("encoded excessive checksum header: %+v", head)
		}
	}
	head, err = sumSizesSqroot(1, maxBlockSize, shortSumLength, sumLength)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encodeSums(bytes.NewReader([]byte{1}), 1, head, cs); err != nil {
		t.Fatalf("valid maximum block size failed: %v", err)
	}
}

func TestMessageHeaderLimits(t *testing.T) {
	tests := []struct {
		tag  byte
		size int
	}{
		{msgInfo, maxMessageBytes + 1},
		{msgError, 0xffffff},
		{msgIOError, 3},
		{msgNoSend, 8},
		{msgSuccess, 0},
		{msgNoop, 1},
		{msgErrorExit, 5},
		{60, 0xffffff},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("tag=%d/size=%d", tt.tag, tt.size), func(t *testing.T) {
			c, _ := newTestConn(t, maxProtocol)
			header := binary.LittleEndian.AppendUint32(nil, uint32(mplexBase+int(tt.tag))<<24|uint32(tt.size))
			c.r.r.Reset(bytes.NewReader(header))
			c.r.multiplex = true
			_, err := c.readInt32()
			if err == nil || errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("header error = %v; want rejection before payload read", err)
			}
		})
	}
}

func TestMessageHeaderBoundaries(t *testing.T) {
	for _, tt := range []struct {
		tag  byte
		size int
	}{{msgIOError, 4}, {msgNoop, 0}, {msgErrorExit, 0}, {msgErrorExit, 4}, {msgInfo, maxMessageBytes}} {
		c, _ := newTestConn(t, maxProtocol)
		wire := binary.LittleEndian.AppendUint32(nil, uint32(mplexBase+int(tt.tag))<<24|uint32(tt.size))
		wire = append(wire, make([]byte, tt.size)...)
		wire = binary.LittleEndian.AppendUint32(wire, uint32(mplexBase)<<24|4)
		wire = binary.LittleEndian.AppendUint32(wire, 42)
		c.r.r.Reset(bytes.NewReader(wire))
		c.r.multiplex = true
		messages := 0
		c.r.onMsg = func(tag byte, data []byte) error {
			messages++
			if tag != tt.tag || len(data) != tt.size {
				t.Fatalf("message %d/%d, want %d/%d", tag, len(data), tt.tag, tt.size)
			}
			return nil
		}
		if got, err := c.readInt32(); err != nil || got != 42 || messages != 1 {
			t.Fatalf("read = %d, %v; messages = %d", got, err, messages)
		}
	}
}

func TestProtectedArgumentLimits(t *testing.T) {
	tests := []struct {
		name, wire string
		wantErr    bool
	}{
		{"normal", "rsync\x00--server\x00site\x00\x00", false},
		{"length-at-limit", "rsync\x00" + strings.Repeat("x", maxArgLength) + "\x00\x00", false},
		{"length-over-limit", "rsync\x00" + strings.Repeat("x", maxArgLength+1), true},
		{"count-at-limit", strings.Repeat("x\x00", maxArgs) + "\x00", false},
		{"count-over-limit", strings.Repeat("x\x00", maxArgs+1), true},
		{"total-over-limit", strings.Repeat(strings.Repeat("x", maxArgLength)+"\x00", maxArgBytes/maxArgLength) + "\x00", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestConn(t, maxProtocol)
			c.r.r.Reset(strings.NewReader(tt.wire))
			_, err := readProtectedArgs(c)
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "argument") {
					t.Fatalf("argument error = %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := validateArgs(make([]string, maxArgs+1)); err == nil {
		t.Fatal("accepted excessive command argument count")
	}
	args := make([]string, maxArgBytes/maxArgLength)
	for i := range args {
		args[i] = strings.Repeat("x", maxArgLength)
	}
	if err := validateArgs(args); err == nil {
		t.Fatal("accepted excessive command argument bytes")
	}
}

func TestFilterListLimits(t *testing.T) {
	for _, tt := range []struct {
		name, rule string
		count      int
		wantErr    bool
	}{
		{"count-at-limit", "- x", maxFilterRules, false},
		{"count-over-limit", "- x", maxFilterRules + 1, true},
		{"bytes-over-limit", "- " + strings.Repeat("x", maxPathLen-1), maxFilterBytes/(maxPathLen+1) + 1, true},
		{"expanded-rules", "-C", maxFilterRules, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var wire []byte
			for range tt.count {
				wire = binary.LittleEndian.AppendUint32(wire, uint32(len(tt.rule)))
				wire = append(wire, tt.rule...)
			}
			wire = binary.LittleEndian.AppendUint32(wire, 0)
			s := newLimitTestSession(t, maxProtocol)
			s.opts.sender = true
			s.c.r.r.Reset(bytes.NewReader(wire))
			err := s.recvFilterList()
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "filter list exceeds metadata limit") {
					t.Fatalf("filter error = %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMergeFilterLimits(t *testing.T) {
	t.Run("rules", func(t *testing.T) {
		l := &filterList{otherSide: ruleReceiverSide}
		if err := l.parse(": .rules", false); err != nil {
			t.Fatal(err)
		}
		read := func(string) ([]byte, error) { return []byte(strings.Repeat("- x\n", maxFilterRules)), nil }
		if _, _, err := l.readMerge(l.merges[0], "site", read); !errors.Is(err, errFilterListLimit) {
			t.Fatalf("merge rule error = %v", err)
		}
	})
	t.Run("bytes", func(t *testing.T) {
		l := &filterList{otherSide: ruleReceiverSide}
		if err := l.parse(": .rules", false); err != nil {
			t.Fatal(err)
		}
		read := func(string) ([]byte, error) { return []byte("#" + strings.Repeat("x", maxFilterBytes/2)), nil }
		if _, _, err := l.readMerge(l.merges[0], "one", read); err != nil {
			t.Fatal(err)
		}
		if _, _, err := l.readMerge(l.merges[0], "two", read); !errors.Is(err, errFilterListLimit) {
			t.Fatalf("merge byte error = %v", err)
		}
	})
	t.Run("scopes", func(t *testing.T) {
		l := &filterList{otherSide: ruleReceiverSide}
		if err := l.parse(": .rules", false); err != nil {
			t.Fatal(err)
		}
		tree := l.newScopeTree("", "", func(string) ([]byte, error) { return nil, nil })
		for i := range maxFileListEntries {
			name := fmt.Sprint(i) + strings.Repeat("x", maxPathLen-16)
			if _, err := tree.scope(name); err != nil {
				if !errors.Is(err, errFilterListLimit) {
					t.Fatal(err)
				}
				return
			}
		}
		t.Fatal("filter scopes exceeded their memory budget")
	})
}

func TestFileListLimits(t *testing.T) {
	for _, tt := range []struct {
		name     string
		count    int
		filename string
		wantErr  bool
	}{
		{"count-at-limit", maxFileListEntries, "x", false},
		{"count-over-limit", maxFileListEntries + 1, "x", true},
		{"bytes-over-limit", maxFileListBytes/(maxPathLen-1) + 1, strings.Repeat("x", maxPathLen-1), true},
		{"normalized-names", maxFileListBytes/(maxPathLen-1) + 1, strings.Repeat("./", (maxPathLen-2)/2) + "x", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newLimitTestSession(t, maxProtocol)
			codec := s.newFlistCodec()
			f := newFileEntry(tt.filename)
			f.mode = sIFREG
			for range tt.count {
				if err := codec.sendEntry(s.c, f); err != nil {
					t.Fatal(err)
				}
			}
			if err := codec.sendEnd(s.c, 0); err != nil {
				t.Fatal(err)
			}
			if err := s.c.flush(); err != nil {
				t.Fatal(err)
			}
			files, err := s.recvFileList()
			if tt.wantErr {
				if !errors.Is(err, errFileListLimit) {
					t.Fatalf("file list error = %v", err)
				}
			} else if err != nil || len(files) != tt.count {
				t.Fatalf("received %d files, %v", len(files), err)
			}
		})
	}
}

func TestFileListOwnerLimit(t *testing.T) {
	s := newLimitTestSession(t, maxProtocol)
	s.opts.numericIDs = false
	s.opts.preserveUID = true
	codec := s.newFlistCodec()
	f := newFileEntry("file")
	f.mode = sIFREG
	if err := codec.sendEntry(s.c, f); err != nil {
		t.Fatal(err)
	}
	if err := codec.sendEnd(s.c, 0); err != nil {
		t.Fatal(err)
	}
	for range 2*maxFileListEntries + 1 {
		if err := s.c.writeVarint30(1); err != nil {
			t.Fatal(err)
		}
		if err := s.c.writeByte(0); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.c.flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.recvFileList(); !errors.Is(err, errFileListLimit) {
		t.Fatalf("owner metadata error = %v", err)
	}
}

type oversizedListingFS struct{ FS }

func (oversizedListingFS) Stat(string) (FileInfo, error) {
	return FileInfo{IsDir: true}, nil
}

func (oversizedListingFS) ReadDir(string, bool) ([]FileInfo, error) {
	return make([]FileInfo, maxFileListEntries+1), nil
}

func TestGeneratedFileListLimit(t *testing.T) {
	s := newLimitTestSession(t, maxProtocol)
	s.fs = oversizedListingFS{}
	s.opts.recurse = true
	s.opts.xferDirs = true
	if _, err := s.buildFileList([]string{"site/"}); !errors.Is(err, errFileListLimit) {
		t.Fatalf("generated file list error = %v", err)
	}
}

func TestExcessiveSignatureFallsBack(t *testing.T) {
	s := newLimitTestSession(t, maxProtocol)
	root := t.TempDir()
	s.fs = dirFS{root: root}
	s.opts.blockSize = 1
	s.opts.maxSize = -1
	s.opts.minSize = -1
	writeFile(t, root+"/file", make([]byte, maxChecksumCount+1))
	f := newFileEntry("file")
	f.mode, f.size = sIFREG, maxChecksumCount+2
	g := generator{s: s}
	if err := g.recvGenerator(0, f, false); err != nil {
		t.Fatal(err)
	}
	if err := s.c.flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.c.readNdx(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.c.readShortint(); err != nil {
		t.Fatal(err)
	}
	head, err := s.c.readSumHead(sumLength)
	if err != nil || head.count != 0 {
		t.Fatalf("fallback checksum header = %+v, %v", head, err)
	}
}

type blockingStatFS struct {
	FS
	started, release chan struct{}
}

func (f blockingStatFS) Stat(string) (FileInfo, error) {
	close(f.started)
	<-f.release
	return FileInfo{}, fs.ErrNotExist
}

func TestReceiverJoinsFailedWorkers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		encoder, buf := newTestConn(t, minProtocol)
		codec := (&session{c: encoder}).newFlistCodec()
		f := newFileEntry("file")
		f.mode, f.size = sIFREG, 1
		if err := codec.sendEntry(encoder, f); err != nil {
			t.Fatal(err)
		}
		if err := codec.sendEnd(encoder, 0); err != nil {
			t.Fatal(err)
		}
		if err := encoder.writeInt32(0); err != nil {
			t.Fatal(err)
		}
		if err := encoder.flush(); err != nil {
			t.Fatal(err)
		}
		input, client := io.Pipe()
		defer func() { _ = client.Close() }()
		rw := struct {
			io.Reader
			io.Writer
		}{input, io.Discard}
		c := newConn(rw)
		defer func() { _ = c.close() }()
		c.protocol = minProtocol
		storage := blockingStatFS{started: make(chan struct{}), release: make(chan struct{})}
		aborted := make(chan struct{})
		s := &session{
			c: c, logger: slog.New(slog.DiscardHandler), fs: storage, filters: &filterList{},
			opts:  options{numericIDs: true, maxSize: -1, minSize: -1},
			abort: func() { _ = input.Close(); close(aborted) },
		}
		done := make(chan error, 1)
		go func() { done <- s.runReceiver(nil) }()
		if _, err := client.Write(buf.Bytes()); err != nil {
			t.Fatal(err)
		}
		<-storage.started
		if _, err := client.Write(binary.LittleEndian.AppendUint32(nil, 99)); err != nil {
			t.Fatal(err)
		}
		<-aborted
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("returned before generator stopped: %v", err)
		default:
		}
		close(storage.release)
		var exit *ExitError
		if err := <-done; !errors.As(err, &exit) || exit.Code != exitProtocol {
			t.Fatalf("receiver error = %v", err)
		}
	})
}

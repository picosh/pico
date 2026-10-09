package rsync

// Protocol versions this implementation speaks. 27 is rsync 2.6.0 and 31 is
// what every rsync since 3.1.0 negotiates down to.
const (
	minProtocol = 27
	maxProtocol = 31
)

// File-list transmission flags (rsync.h XMIT_*).
const (
	xmitTopDir           = 1 << 0
	xmitSameMode         = 1 << 1
	xmitSameRdevPre28    = 1 << 2
	xmitExtendedFlags    = 1 << 2
	xmitSameUID          = 1 << 3
	xmitSameGID          = 1 << 4
	xmitSameName         = 1 << 5
	xmitLongName         = 1 << 6
	xmitSameTime         = 1 << 7
	xmitSameRdevMajor    = 1 << 8
	xmitNoContentDir     = 1 << 8
	xmitHlinked          = 1 << 9
	xmitUserNameFollows  = 1 << 10
	xmitRdevMinor8Pre30  = 1 << 11
	xmitGroupNameFollows = 1 << 11
	xmitHlinkFirst       = 1 << 12
	xmitIOErrorEndList   = 1 << 12
	xmitModNsec          = 1 << 13
	xmitSameAtime        = 1 << 14
)

// Sizes and limits (rsync.h).
const (
	maxPathLen           = 4096
	maxWireNsec          = 999999999
	blockSize            = 700
	maxBlockSize         = 1 << 17
	oldMaxBlockSize      = 1 << 29
	chunkSize            = 32 * 1024
	maxMapSize           = 256 * 1024
	sumLength            = 16
	shortSumLength       = 2
	blocksumBias         = 10
	maxDataCount         = 16383
	mplexBase            = 7
	maxChainLen          = 1024
	traditionalTableSize = 1 << 16
)

// Unix file type bits as sent on the wire.
const (
	sIFMT   = 0o170000
	sIFSOCK = 0o140000
	sIFLNK  = 0o120000
	sIFREG  = 0o100000
	sIFBLK  = 0o060000
	sIFDIR  = 0o040000
	sIFCHR  = 0o020000
	sIFIFO  = 0o010000
)

// Item flags sent alongside a file index (rsync.h ITEM_*).
const (
	itemReportChange     = 1 << 1
	itemReportSize       = 1 << 2
	itemReportTime       = 1 << 3
	itemBasisTypeFollows = 1 << 11
	itemXnameFollows     = 1 << 12
	itemIsNew            = 1 << 13
	itemTransfer         = 1 << 15
	itemMissingData      = 1 << 16
)

// Basis file types sent with itemBasisTypeFollows.
const (
	fnamecmpFname      = 0x80
	fnamecmpPartialDir = 0x81
)

// Multiplexed message tags (rsync.h enum msgcode).
const (
	msgData      = 0
	msgErrorXfer = 1
	msgInfo      = 2
	msgError     = 3
	msgWarning   = 4
	msgIOError   = 22
	msgNoop      = 42
	msgErrorExit = 86
	msgSuccess   = 100
	msgDeleted   = 101
	msgNoSend    = 102
)

// Special file-list index values.
const (
	ndxDone     = -1
	ndxFlistEOF = -2
	ndxDelStats = -3
)

// I/O error bits reported at the end of the file list.
const (
	ioErrGeneral  = 1 << 0
	ioErrVanished = 1 << 1
	ioErrDelLimit = 1 << 2
	ioErrMask     = ioErrGeneral | ioErrVanished | ioErrDelLimit
)

// Compat flags a protocol 30+ server sends. We never offer incremental
// recursion, symlink times or iconv.
const (
	cfSafeFlist         = 1 << 3
	cfAvoidXattrOptim   = 1 << 4
	cfChksumSeedFix     = 1 << 5
	cfInplacePartialDir = 1 << 6
	cfVarintFlistFlags  = 1 << 7
	cfID0Names          = 1 << 8
)

// Exit codes (errcode.h).
const (
	exitSyntax      = 1
	exitProtocol    = 2
	exitFileSelect  = 3
	exitUnsupported = 4
	exitFileIO      = 11
	exitStreamIO    = 12
	exitPartial     = 23
	exitVanished    = 24
	exitDelLimit    = 25
)

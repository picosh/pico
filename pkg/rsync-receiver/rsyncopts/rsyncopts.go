// Package rsyncopts implements a parser for command-line options that
// implements a subset of popt(3) semantics; just enough to parse typical
// rsync(1) invocations without the advanced popt features like aliases
// or option prefix matching (not --del, only --delete).
//
// If we encounter arguments that rsync(1) parses differently compared to this
// package, then this package should be adjusted to match rsync(1).
package rsyncopts

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unicode"
)

const (
	OPT_SERVER = 1000 + iota
	OPT_DAEMON
	OPT_SENDER
	OPT_EXCLUDE
	OPT_EXCLUDE_FROM
	OPT_FILTER
	OPT_COMPARE_DEST
	OPT_COPY_DEST
	OPT_LINK_DEST
	OPT_HELP
	OPT_INCLUDE
	OPT_INCLUDE_FROM
	OPT_MODIFY_WINDOW
	OPT_MIN_SIZE
	OPT_CHMOD
	OPT_READ_BATCH
	OPT_WRITE_BATCH
	OPT_ONLY_WRITE_BATCH
	OPT_MAX_SIZE
	OPT_NO_D
	OPT_APPEND
	OPT_NO_ICONV
	OPT_INFO
	OPT_DEBUG
	OPT_BLOCK_SIZE
	OPT_USERMAP
	OPT_GROUPMAP
	OPT_CHOWN
	OPT_BWLIMIT
	OPT_STDERR
	OPT_OLD_COMPRESS
	OPT_NEW_COMPRESS
	OPT_NO_COMPRESS
	OPT_OLD_ARGS
	OPT_STOP_AFTER
	OPT_STOP_AT
	OPT_REFUSED_BASE = 9000
)

type infoLevel int

const (
	INFO_BACKUP infoLevel = iota
	INFO_COPY
	INFO_DEL
	INFO_FLIST
	INFO_MISC
	INFO_MOUNT
	INFO_NAME
	INFO_NONREG
	INFO_PROGRESS
	INFO_REMOVE
	INFO_SKIP
	INFO_STATS
	INFO_SYMSAFE
	COUNT_INFO
)

// NewOptions returns an Options struct with all options initialized to their
// default values. Note that ParseArguments will set some options (that default
// to -1) based on the encountered command-line flags and built-in rules.
func NewOptions() *Options {
	return &Options{
		msgs2stderr:          2, // Default: send errors to stderr for local & remote-shell transfers
		output_motd:          1,
		human_readable:       1,
		allow_inc_recurse:    1,
		xfer_dirs:            -1,
		relative_paths:       -1,
		implied_dirs:         1,
		max_delete:           math.MinInt32,
		whole_file:           -1,
		do_compression_level: math.MinInt32,
		rsync_path:           "rsync",
		default_af_hint:      syscall.AF_INET6,
		blocking_io:          -1,
		protocol_version:     27,
	}
}

// GokrazyOptions contains additional command-line flags, prefixed with
// gokr. (like --gokr.modulemap) to not clash with rsync flag names.
type GokrazyOptions struct {
	Config           string
	Listen           string
	MonitoringListen string
	AnonSSHListen    string
	ModuleMap        string
}

func (o *GokrazyOptions) table() []poptOption {
	return []poptOption{
		/* longName, shortName, argInfo, arg, val */
		{"gokr.config", "", POPT_ARG_STRING, &o.Config, 0},
		{"gokr.listen", "", POPT_ARG_STRING, &o.Listen, 0},
		{"gokr.monitoring_listen", "", POPT_ARG_STRING, &o.MonitoringListen, 0},
		{"gokr.anonssh_listen", "", POPT_ARG_STRING, &o.AnonSSHListen, 0},
		{"gokr.modulemap", "", POPT_ARG_STRING, &o.ModuleMap, 0},
	}
}

type Options struct {
	Gokrazy GokrazyOptions

	// not directly referenced in the table, but used in the special case code.
	do_compression int
	info           [COUNT_INFO]uint16
	local_server   int

	// order matches long_options order
	verbose                int
	msgs2stderr            int
	quiet                  int
	output_motd            int
	do_stats               int
	human_readable         int
	dry_run                int
	recurse                int
	allow_inc_recurse      int
	xfer_dirs              int
	preserve_perms         int
	preserve_executability int
	preserve_acls          int
	preserve_xattrs        int
	preserve_mtimes        int
	preserve_atimes        int
	open_noatime           int
	preserve_crtimes       int
	omit_dir_times         int
	omit_link_times        int
	modify_window          int
	am_root                int // 0 = normal, 1 = root, 2 = --super, -1 = --fake-super
	preserve_uid           int
	preserve_gid           int
	preserve_devices       int
	copy_devices           int
	write_devices          int
	preserve_specials      int
	preserve_links         int
	copy_links             int
	copy_unsafe_links      int
	safe_symlinks          int
	munge_symlinks         int
	copy_dirlinks          int
	keep_dirlinks          int
	preserve_hard_links    int
	relative_paths         int
	implied_dirs           int
	ignore_times           int
	size_only              int
	one_file_system        int
	update_only            int
	ignore_non_existing    int
	ignore_existing        int
	max_size_arg           string
	min_size_arg           string
	max_alloc_arg          string
	sparse_files           int
	preallocate_files      int
	inplace                int
	append_mode            int
	delete_during          int
	delete_mode            int
	delete_before          int
	delete_after           int
	delete_excluded        int
	missing_args           int // 0 = FERROR_XFER, 1 = ignore, 2 = delete
	remove_source_files    int
	force_delete           int
	ignore_errors          int
	max_delete             int
	cvs_exclude            int
	// If 1, send the whole file as literal data rather than trying to create an
	// incremental diff.
	// If -1, then look at whether we're local or remote and go by that.
	// See also disable_deltas_p()
	whole_file           int
	always_checksum      int
	checksum_choice      string
	fuzzy_basis          int
	compress_choice      string
	skip_compress        string
	do_compression_level int
	do_progress          int
	keep_partial         int
	partial_dir          string
	delay_updates        int
	prune_empty_dirs     int
	logfile_name         string
	logfile_format       string
	stdout_format        string
	itemize_changes      int
	bwlimit_arg          string
	bwlimit              int
	make_backups         int
	backup_dir           string
	backup_suffix        string
	list_only            int
	batch_name           string
	files_from           string
	eol_nulls            int
	old_style_args       int // intentionally set to 0; unsupported
	protect_args         int // intentionally set to 0; currently unsupported
	trust_sender         int
	numeric_ids          int
	io_timeout           int
	connect_timeout      int
	do_fsync             int
	shell_cmd            string
	rsync_path           string
	tmpdir               string
	iconv_opt            string
	default_af_hint      int
	allow_8bit_chars     int
	mkpath_dest_arg      int
	use_qsort            int
	copy_as              string
	bind_address         string // numeric IPv4 or IPv6, or a hostname
	rsync_port           int
	sockopts             string
	password_file        string
	early_input_file     string
	blocking_io          int
	outbuf_mode          string
	protocol_version     int
	checksum_seed        int
	am_server            int
	am_sender            int
	am_daemon            int

	daemon_bwlimit int
	config_file    string
	daemon_opt     int
	no_detach      int
}

type priority int

const (
	DEFAULT_PRIORITY priority = iota
	HELP_PRIORITY
	USER_PRIORITY
	LIMIT_PRIORITY
)

const (
	W_CLI = 1 << iota
	W_SRV
	W_SND
	W_REC
)

type output struct {
	name  string
	where int
	help  string
}

var infoWords = [...]output{
	{"BACKUP", W_REC, "Mention files backed up"},
	{"COPY", W_REC, "Mention files copied locally on the receiving side"},
	{"DEL", W_REC, "Mention deletions on the receiving side"},
	{"FLIST", W_CLI, "Mention file-list receiving/sending (levels 1-2)"},
	{"MISC", W_SND | W_REC, "Mention miscellaneous information (levels 1-2)"},
	{"MOUNT", W_SND | W_REC, "Mention mounts that were found or skipped"},
	{"NAME", W_SND | W_REC, "Mention 1) updated file/dir names, 2) unchanged names"},
	{"NONREG", W_REC, "Mention skipped non-regular files (default 1, 0 disables)"},
	{"PROGRESS", W_CLI, "Mention 1) per-file progress or 2) total transfer progress"},
	{"REMOVE", W_SND, "Mention files removed on the sending side"},
	{"SKIP", W_REC, "Mention files skipped due to transfer overrides (levels 1-2)"},
	{"STATS", W_CLI | W_SRV, "Mention statistics at end of run (levels 1-3)"},
	{"SYMSAFE", W_SND | W_REC, "Mention symlinks that are unsafe"},
}

func parseOutputWords(words []output, levels []uint16, str string, prio priority) {
Level:
	for s := range strings.SplitSeq(str, ",") {
		if strings.TrimSpace(s) == "" {
			continue
		}
		trimmed := strings.TrimRightFunc(s, unicode.IsNumber)
		lev := 1
		if len(trimmed) < len(s) {
			var err error
			lev, err = strconv.Atoi(s[len(trimmed):])
			if err != nil {
				continue
			}
		}
		trimmed = strings.ToLower(trimmed)
		all := false
		switch trimmed {
		case "none":
			lev = 0
		case "all":
			all = true
		}
		for j := range words {
			word := words[j]
			if strings.ToLower(word.name) == trimmed || all {
				levels[j] = uint16(lev)
				if !all {
					continue Level
				}
			}
		}
	}
}

func (o *Options) setOutputVerbosity(prio priority) {
	// debugVerbosity is reserved for future use with debug logging levels.
	// debugVerbosity := [...]string{
	//     "",
	//     "",
	//     "BIND,CMD,CONNECT,DEL,DELTASUM,DUP,FILTER,FLIST,ICONV",
	//     "ACL,BACKUP,CONNECT2,DELTASUM2,DEL2,EXIT,FILTER2,FLIST2,FUZZY,GENR,OWN,RECV,SEND,TIME",
	//     "CMD2,DELTASUM3,DEL3,EXIT2,FLIST3,ICONV2,OWN2,PROTO,TIME2",
	//     "CHDIR,DELTASUM4,FLIST4,FUZZY2,HASH,HLINK",
	// }
	infoVerbosity := [...]string{
		"NONREG",
		"COPY,DEL,FLIST,MISC,NAME,STATS,SYMSAFE",
		"BACKUP,MISC2,MOUNT,NAME2,REMOVE,SKIP",
	}
	for j := 0; j <= o.verbose; j++ {
		if j < len(infoVerbosity) {
			parseOutputWords(infoWords[:], o.info[:], infoVerbosity[j], prio)
		}
		// TODO: enable debug verbosity when debug logging is implemented
		// if j < len(debugVerbosity) {
		//     parseOutputWords(debugWords[:], o.debug[:], debugVerbosity[j], prio)
		// }
	}
}

func (o *Options) Help() string {
	return ""
}

func (o *Options) ShellCommand() string       { return o.shell_cmd }
func (o *Options) UpdateOnly() bool           { return o.update_only != 0 }
func (o *Options) DryRun() bool               { return o.dry_run != 0 }
func (o *Options) PreserveLinks() bool        { return o.preserve_links != 0 }
func (o *Options) PreserveUid() bool          { return o.preserve_uid != 0 }
func (o *Options) PreserveGid() bool          { return o.preserve_gid != 0 }
func (o *Options) PreserveDevices() bool      { return o.preserve_devices != 0 }
func (o *Options) PreserveMTimes() bool       { return o.preserve_mtimes != 0 }
func (o *Options) PreservePerms() bool        { return o.preserve_perms != 0 }
func (o *Options) PreserveSpecials() bool     { return o.preserve_specials != 0 }
func (o *Options) PreserveHardLinks() bool    { return o.preserve_hard_links != 0 }
func (o *Options) Recurse() bool              { return o.recurse != 0 }
func (o *Options) Verbose() bool              { return o.verbose != 0 }
func (o *Options) DeleteMode() bool           { return o.delete_mode != 0 }
func (o *Options) Sender() bool               { return o.am_sender != 0 }
func (o *Options) SetSender()                 { o.am_sender = 1 }
func (o *Options) LocalServer() bool          { return o.local_server != 0 }
func (o *Options) SetLocalServer()            { o.local_server = 1 }
func (o *Options) Server() bool               { return o.am_server != 0 }
func (o *Options) Daemon() bool               { return o.am_daemon != 0 }
func (o *Options) ConnectTimeoutSeconds() int { return o.connect_timeout }
func (o *Options) AlwaysChecksum() bool       { return o.always_checksum != 0 }
func (o *Options) Compress() bool             { return o.do_compression != 0 }
func (o *Options) CompressChoice() string     { return o.compress_choice }
func (o *Options) CompressLevel() int         { return o.do_compression_level }
func (o *Options) IgnoreTimes() bool          { return o.ignore_times == 1 }
func (o *Options) SizeOnly() bool             { return o.size_only == 1 }

func (o *Options) daemonTable() []poptOption {
	return []poptOption{
		/* longName, shortName, argInfo, arg, val */
		{"help", "", POPT_ARG_NONE, nil, OPT_HELP},
		{"address", "", POPT_ARG_STRING, &o.bind_address, 0},
		{"bwlimit", "", POPT_ARG_INT, &o.daemon_bwlimit, 0},
		{"config", "", POPT_ARG_STRING, &o.config_file, 0},
		{"daemon", "", POPT_ARG_NONE, &o.daemon_opt, 0},
		{"dparam", "M", POPT_ARG_STRING, nil, 'M'},
		{"ipv4", "4", POPT_ARG_VAL, &o.default_af_hint, syscall.AF_INET},
		{"ipv6", "6", POPT_ARG_VAL, &o.default_af_hint, syscall.AF_INET6},
		{"detach", "", POPT_ARG_VAL, &o.no_detach, 0},
		{"no-detach", "", POPT_ARG_VAL, &o.no_detach, 1},
		{"log-file", "", POPT_ARG_STRING, &o.logfile_name, 0},
		{"log-file-format", "", POPT_ARG_STRING, &o.logfile_format, 0},
		{"port", "", POPT_ARG_INT, &o.rsync_port, 0},
		{"sockopts", "", POPT_ARG_STRING, &o.sockopts, 0},
		{"protocol", "", POPT_ARG_INT, &o.protocol_version, 0},
		{"server", "", POPT_ARG_NONE, &o.am_server, 0},
		{"temp-dir", "T", POPT_ARG_STRING, &o.tmpdir, 0},
		{"verbose", "v", POPT_ARG_NONE, 0, 'v'},
		{"no-verbose", "", POPT_ARG_VAL, &o.verbose, 0},
		{"no-v", "", POPT_ARG_VAL, &o.verbose, 0},
		{"help", "h", POPT_ARG_NONE, 0, 'h'},
	}
}

func (o *Options) table() []poptOption {
	return []poptOption{
		/* longName, shortName, argInfo, arg, val */
		{"help", "", POPT_ARG_NONE, nil, OPT_HELP},
		{"version", "V", POPT_ARG_NONE, nil, 'V'},
		{"verbose", "v", POPT_ARG_NONE, nil, 'v'},
		{"no-verbose", "", POPT_ARG_VAL, &o.verbose, 0},
		{"no-v", "", POPT_ARG_VAL, &o.verbose, 0},
		{"info", "", POPT_ARG_STRING, nil, OPT_INFO},
		{"debug", "", POPT_ARG_STRING, nil, OPT_DEBUG},
		{"stderr", "", POPT_ARG_STRING, nil, OPT_STDERR},
		{"msgs2stderr", "", POPT_ARG_VAL, &o.msgs2stderr, 1},
		{"no-msgs2stderr", "", POPT_ARG_VAL, &o.msgs2stderr, 0},
		{"quiet", "q", POPT_ARG_NONE, nil, 'q'},
		{"motd", "", POPT_ARG_VAL, &o.output_motd, 1},
		{"no-motd", "", POPT_ARG_VAL, &o.output_motd, 0},
		{"stats", "", POPT_ARG_NONE, &o.do_stats, 0},
		{"human-readable", "h", POPT_ARG_NONE, nil, 'h'},
		{"no-human-readable", "", POPT_ARG_VAL, &o.human_readable, 0},
		{"no-h", "", POPT_ARG_VAL, &o.human_readable, 0},
		{"dry-run", "n", POPT_ARG_NONE, &o.dry_run, 0},
		{"archive", "a", POPT_ARG_NONE, nil, 'a'},
		{"recursive", "r", POPT_ARG_VAL, &o.recurse, 2},
		{"no-recursive", "", POPT_ARG_VAL, &o.recurse, 0},
		{"no-r", "", POPT_ARG_VAL, &o.recurse, 0},
		{"inc-recursive", "", POPT_ARG_VAL, &o.allow_inc_recurse, 1},
		{"no-inc-recursive", "", POPT_ARG_VAL, &o.allow_inc_recurse, 0},
		{"i-r", "", POPT_ARG_VAL, &o.allow_inc_recurse, 1},
		{"no-i-r", "", POPT_ARG_VAL, &o.allow_inc_recurse, 0},
		{"dirs", "d", POPT_ARG_VAL, &o.xfer_dirs, 2},
		{"no-dirs", "", POPT_ARG_VAL, &o.xfer_dirs, 0},
		{"no-d", "", POPT_ARG_VAL, &o.xfer_dirs, 0},
		{"old-dirs", "", POPT_ARG_VAL, &o.xfer_dirs, 4},
		{"old-d", "", POPT_ARG_VAL, &o.xfer_dirs, 4},
		{"perms", "p", POPT_ARG_VAL, &o.preserve_perms, 1},
		{"no-perms", "", POPT_ARG_VAL, &o.preserve_perms, 0},
		{"no-p", "", POPT_ARG_VAL, &o.preserve_perms, 0},
		{"executability", "E", POPT_ARG_NONE, &o.preserve_executability, 0},
		{"acls", "A", POPT_ARG_NONE, nil, 'A'},
		{"no-acls", "", POPT_ARG_VAL, &o.preserve_acls, 0},
		{"no-A", "", POPT_ARG_VAL, &o.preserve_acls, 0},
		{"xattrs", "X", POPT_ARG_NONE, nil, 'X'},
		{"no-xattrs", "", POPT_ARG_VAL, &o.preserve_xattrs, 0},
		{"no-X", "", POPT_ARG_VAL, &o.preserve_xattrs, 0},
		{"times", "t", POPT_ARG_VAL, &o.preserve_mtimes, 1},
		{"no-times", "", POPT_ARG_VAL, &o.preserve_mtimes, 0},
		{"no-t", "", POPT_ARG_VAL, &o.preserve_mtimes, 0},
		{"atimes", "U", POPT_ARG_NONE, nil, 'U'},
		{"no-atimes", "", POPT_ARG_VAL, &o.preserve_atimes, 0},
		{"no-U", "", POPT_ARG_VAL, &o.preserve_atimes, 0},
		{"open-noatime", "", POPT_ARG_VAL, &o.open_noatime, 1},
		{"no-open-noatime", "", POPT_ARG_VAL, &o.open_noatime, 0},
		{"crtimes", "N", POPT_ARG_NONE, &o.preserve_crtimes, 1}, // refused
		{"no-crtimes", "", POPT_ARG_VAL, &o.preserve_crtimes, 0},
		{"no-N", "", POPT_ARG_VAL, &o.preserve_crtimes, 0},
		{"omit-dir-times", "O", POPT_ARG_VAL, &o.omit_dir_times, 1},
		{"no-omit-dir-times", "", POPT_ARG_VAL, &o.omit_dir_times, 0},
		{"no-O", "", POPT_ARG_VAL, &o.omit_dir_times, 0},
		{"omit-link-times", "J", POPT_ARG_VAL, &o.omit_link_times, 1},
		{"no-omit-link-times", "", POPT_ARG_VAL, &o.omit_link_times, 0},
		{"no-J", "", POPT_ARG_VAL, &o.omit_link_times, 0},
		{"modify-window", "@", POPT_ARG_INT, &o.modify_window, OPT_MODIFY_WINDOW},
		{"super", "", POPT_ARG_VAL, &o.am_root, 2},
		{"no-super", "", POPT_ARG_VAL, &o.am_root, 0},
		{"fake-super", "", POPT_ARG_VAL, &o.am_root, -1},
		{"owner", "o", POPT_ARG_VAL, &o.preserve_uid, 1},
		{"no-owner", "", POPT_ARG_VAL, &o.preserve_uid, 0},
		{"no-o", "", POPT_ARG_VAL, &o.preserve_uid, 0},
		{"group", "g", POPT_ARG_VAL, &o.preserve_gid, 1},
		{"no-group", "", POPT_ARG_VAL, &o.preserve_gid, 0},
		{"no-g", "", POPT_ARG_VAL, &o.preserve_gid, 0},
		{"", "D", POPT_ARG_NONE, nil, 'D'},
		{"no-D", "", POPT_ARG_NONE, nil, OPT_NO_D},
		{"devices", "", POPT_ARG_VAL, &o.preserve_devices, 1},
		{"no-devices", "", POPT_ARG_VAL, &o.preserve_devices, 0},
		{"copy-devices", "", POPT_ARG_NONE, &o.copy_devices, 0},
		{"write-devices", "", POPT_ARG_VAL, &o.write_devices, 1},
		{"no-write-devices", "", POPT_ARG_VAL, &o.write_devices, 0},
		{"specials", "", POPT_ARG_VAL, &o.preserve_specials, 1},
		{"no-specials", "", POPT_ARG_VAL, &o.preserve_specials, 0},
		{"links", "l", POPT_ARG_VAL, &o.preserve_links, 1},
		{"no-links", "", POPT_ARG_VAL, &o.preserve_links, 0},
		{"no-l", "", POPT_ARG_VAL, &o.preserve_links, 0},
		{"copy-links", "L", POPT_ARG_NONE, &o.copy_links, 0},
		{"copy-unsafe-links", "", POPT_ARG_NONE, &o.copy_unsafe_links, 0},
		{"safe-links", "", POPT_ARG_NONE, &o.safe_symlinks, 0},
		{"munge-links", "", POPT_ARG_VAL, &o.munge_symlinks, 1},
		{"no-munge-links", "", POPT_ARG_VAL, &o.munge_symlinks, 0},
		{"copy-dirlinks", "k", POPT_ARG_NONE, &o.copy_dirlinks, 0},
		{"keep-dirlinks", "K", POPT_ARG_NONE, &o.keep_dirlinks, 0},
		{"hard-links", "H", POPT_ARG_NONE, nil, 'H'},
		{"no-hard-links", "", POPT_ARG_VAL, &o.preserve_hard_links, 0},
		{"no-H", "", POPT_ARG_VAL, &o.preserve_hard_links, 0},
		{"relative", "R", POPT_ARG_VAL, &o.relative_paths, 1},
		{"no-relative", "", POPT_ARG_VAL, &o.relative_paths, 0},
		{"no-R", "", POPT_ARG_VAL, &o.relative_paths, 0},
		{"implied-dirs", "", POPT_ARG_VAL, &o.implied_dirs, 1},
		{"no-implied-dirs", "", POPT_ARG_VAL, &o.implied_dirs, 0},
		{"i-d", "", POPT_ARG_VAL, &o.implied_dirs, 1},
		{"no-i-d", "", POPT_ARG_VAL, &o.implied_dirs, 0},
		{"chmod", "", POPT_ARG_STRING, nil, OPT_CHMOD},
		{"ignore-times", "I", POPT_ARG_NONE, &o.ignore_times, 0},
		{"size-only", "", POPT_ARG_NONE, &o.size_only, 0},
		{"one-file-system", "x", POPT_ARG_NONE, nil, 'x'},
		{"no-one-file-system", "", POPT_ARG_VAL, &o.one_file_system, 0},
		{"no-x", "", POPT_ARG_VAL, &o.one_file_system, 0},
		{"update", "u", POPT_ARG_NONE, &o.update_only, 0},
		{"existing", "", POPT_ARG_NONE, &o.ignore_non_existing, 0},
		{"ignore-non-existing", "", POPT_ARG_NONE, &o.ignore_non_existing, 0},
		{"ignore-existing", "", POPT_ARG_NONE, &o.ignore_existing, 0},
		{"max-size", "", POPT_ARG_STRING, &o.max_size_arg, OPT_MAX_SIZE},
		{"min-size", "", POPT_ARG_STRING, &o.min_size_arg, OPT_MIN_SIZE},
		{"max-alloc", "", POPT_ARG_STRING, &o.max_alloc_arg, 0},
		{"sparse", "S", POPT_ARG_VAL, &o.sparse_files, 1},
		{"no-sparse", "", POPT_ARG_VAL, &o.sparse_files, 0},
		{"no-S", "", POPT_ARG_VAL, &o.sparse_files, 0},
		{"preallocate", "", POPT_ARG_NONE, &o.preallocate_files, 0},
		{"inplace", "", POPT_ARG_VAL, &o.inplace, 1},
		{"no-inplace", "", POPT_ARG_VAL, &o.inplace, 0},
		{"append", "", POPT_ARG_NONE, nil, OPT_APPEND},
		{"append-verify", "", POPT_ARG_VAL, &o.append_mode, 2},
		{"no-append", "", POPT_ARG_VAL, &o.append_mode, 0},
		{"del", "", POPT_ARG_NONE, &o.delete_during, 0},
		{"delete", "", POPT_ARG_NONE, &o.delete_mode, 0},
		{"delete-before", "", POPT_ARG_NONE, &o.delete_before, 0},
		{"delete-during", "", POPT_ARG_VAL, &o.delete_during, 1},
		{"delete-delay", "", POPT_ARG_VAL, &o.delete_during, 2},
		{"delete-after", "", POPT_ARG_NONE, &o.delete_after, 0},
		{"delete-excluded", "", POPT_ARG_NONE, &o.delete_excluded, 0},
		{"delete-missing-args", "", POPT_BIT_SET, &o.missing_args, 2},
		{"ignore-missing-args", "", POPT_BIT_SET, &o.missing_args, 1},
		{"remove-sent-files", "", POPT_ARG_VAL, &o.remove_source_files, 2}, /* deprecated */
		{"remove-source-files", "", POPT_ARG_VAL, &o.remove_source_files, 1},
		{"force", "", POPT_ARG_VAL, &o.force_delete, 1},
		{"no-force", "", POPT_ARG_VAL, &o.force_delete, 0},
		{"ignore-errors", "", POPT_ARG_VAL, &o.ignore_errors, 1},
		{"no-ignore-errors", "", POPT_ARG_VAL, &o.ignore_errors, 0},
		{"max-delete", "", POPT_ARG_INT, &o.max_delete, 0},
		{"", "F", POPT_ARG_NONE, nil, 'F'},
		{"filter", "f", POPT_ARG_STRING, nil, OPT_FILTER},
		{"exclude", "", POPT_ARG_STRING, nil, OPT_EXCLUDE},
		{"include", "", POPT_ARG_STRING, nil, OPT_INCLUDE},
		{"exclude-from", "", POPT_ARG_STRING, nil, OPT_EXCLUDE_FROM},
		{"include-from", "", POPT_ARG_STRING, nil, OPT_INCLUDE_FROM},
		{"cvs-exclude", "C", POPT_ARG_NONE, &o.cvs_exclude, 0},
		{"whole-file", "W", POPT_ARG_VAL, &o.whole_file, 1},
		{"no-whole-file", "", POPT_ARG_VAL, &o.whole_file, 0},
		{"no-W", "", POPT_ARG_VAL, &o.whole_file, 0},
		{"checksum", "c", POPT_ARG_VAL, &o.always_checksum, 1},
		{"no-checksum", "", POPT_ARG_VAL, &o.always_checksum, 0},
		{"no-c", "", POPT_ARG_VAL, &o.always_checksum, 0},
		{"checksum-choice", "", POPT_ARG_STRING, &o.checksum_choice, 0},
		{"cc", "", POPT_ARG_STRING, &o.checksum_choice, 0},
		{"block-size", "B", POPT_ARG_STRING, nil, OPT_BLOCK_SIZE},
		{"compare-dest", "", POPT_ARG_STRING, nil, OPT_COMPARE_DEST},
		{"copy-dest", "", POPT_ARG_STRING, nil, OPT_COPY_DEST},
		{"link-dest", "", POPT_ARG_STRING, nil, OPT_LINK_DEST},
		{"fuzzy", "y", POPT_ARG_NONE, nil, 'y'},
		{"no-fuzzy", "", POPT_ARG_VAL, &o.fuzzy_basis, 0},
		{"no-y", "", POPT_ARG_VAL, &o.fuzzy_basis, 0},
		{"compress", "z", POPT_ARG_NONE, nil, 'z'},
		{"old-compress", "", POPT_ARG_NONE, nil, OPT_OLD_COMPRESS},
		{"new-compress", "", POPT_ARG_NONE, nil, OPT_NEW_COMPRESS},
		{"no-compress", "", POPT_ARG_NONE, nil, OPT_NO_COMPRESS},
		{"no-z", "", POPT_ARG_NONE, nil, OPT_NO_COMPRESS},
		{"compress-choice", "", POPT_ARG_STRING, &o.compress_choice, 0},
		{"zc", "", POPT_ARG_STRING, &o.compress_choice, 0},
		{"skip-compress", "", POPT_ARG_STRING, &o.skip_compress, 0},
		{"compress-level", "", POPT_ARG_INT, &o.do_compression_level, 0},
		{"zl", "", POPT_ARG_INT, &o.do_compression_level, 0},
		{"", "P", POPT_ARG_NONE, nil, 'P'},
		{"progress", "", POPT_ARG_VAL, &o.do_progress, 1},
		{"no-progress", "", POPT_ARG_VAL, &o.do_progress, 0},
		{"partial", "", POPT_ARG_VAL, &o.keep_partial, 1},
		{"no-partial", "", POPT_ARG_VAL, &o.keep_partial, 0},
		{"partial-dir", "", POPT_ARG_STRING, &o.partial_dir, 0},
		{"delay-updates", "", POPT_ARG_VAL, &o.delay_updates, 1},
		{"no-delay-updates", "", POPT_ARG_VAL, &o.delay_updates, 0},
		{"prune-empty-dirs", "m", POPT_ARG_VAL, &o.prune_empty_dirs, 1},
		{"no-prune-empty-dirs", "", POPT_ARG_VAL, &o.prune_empty_dirs, 0},
		{"no-m", "", POPT_ARG_VAL, &o.prune_empty_dirs, 0},
		{"log-file", "", POPT_ARG_STRING, &o.logfile_name, 0},
		{"log-file-format", "", POPT_ARG_STRING, &o.logfile_format, 0},
		{"out-format", "", POPT_ARG_STRING, &o.stdout_format, 0},
		{"log-format", "", POPT_ARG_STRING, &o.stdout_format, 0}, /* DEPRECATED */
		{"itemize-changes", "i", POPT_ARG_NONE, nil, 'i'},
		{"no-itemize-changes", "", POPT_ARG_VAL, &o.itemize_changes, 0},
		{"no-i", "", POPT_ARG_VAL, &o.itemize_changes, 0},
		{"bwlimit", "", POPT_ARG_STRING, &o.bwlimit_arg, OPT_BWLIMIT},
		{"no-bwlimit", "", POPT_ARG_VAL, &o.bwlimit, 0},
		{"backup", "b", POPT_ARG_VAL, &o.make_backups, 1},
		{"no-backup", "", POPT_ARG_VAL, &o.make_backups, 0},
		{"backup-dir", "", POPT_ARG_STRING, &o.backup_dir, 0},
		{"suffix", "", POPT_ARG_STRING, &o.backup_suffix, 0},
		{"list-only", "", POPT_ARG_VAL, &o.list_only, 2},
		{"read-batch", "", POPT_ARG_STRING, &o.batch_name, OPT_READ_BATCH},
		{"write-batch", "", POPT_ARG_STRING, &o.batch_name, OPT_WRITE_BATCH},
		{"only-write-batch", "", POPT_ARG_STRING, &o.batch_name, OPT_ONLY_WRITE_BATCH},
		{"files-from", "", POPT_ARG_STRING, &o.files_from, 0},
		{"from0", "0", POPT_ARG_VAL, &o.eol_nulls, 1},
		{"no-from0", "", POPT_ARG_VAL, &o.eol_nulls, 0},
		{"old-args", "", POPT_ARG_NONE, nil, OPT_OLD_ARGS},
		{"no-old-args", "", POPT_ARG_VAL, &o.old_style_args, 0},
		{"secluded-args", "s", POPT_ARG_VAL, &o.protect_args, 1},
		{"no-secluded-args", "", POPT_ARG_VAL, &o.protect_args, 0},
		{"protect-args", "", POPT_ARG_VAL, &o.protect_args, 1},
		{"no-protect-args", "", POPT_ARG_VAL, &o.protect_args, 0},
		{"no-s", "", POPT_ARG_VAL, &o.protect_args, 0},
		{"trust-sender", "", POPT_ARG_VAL, &o.trust_sender, 1},
		{"numeric-ids", "", POPT_ARG_VAL, &o.numeric_ids, 1},
		{"no-numeric-ids", "", POPT_ARG_VAL, &o.numeric_ids, 0},
		{"usermap", "", POPT_ARG_STRING, nil, OPT_USERMAP},
		{"groupmap", "", POPT_ARG_STRING, nil, OPT_GROUPMAP},
		{"chown", "", POPT_ARG_STRING, nil, OPT_CHOWN},
		{"timeout", "", POPT_ARG_INT, &o.io_timeout, 0},
		{"no-timeout", "", POPT_ARG_VAL, &o.io_timeout, 0},
		{"contimeout", "", POPT_ARG_INT, &o.connect_timeout, 0},
		{"no-contimeout", "", POPT_ARG_VAL, &o.connect_timeout, 0},
		{"fsync", "", POPT_ARG_NONE, &o.do_fsync, 0},
		{"stop-after", "", POPT_ARG_STRING, nil, OPT_STOP_AFTER},
		{"time-limit", "", POPT_ARG_STRING, nil, OPT_STOP_AFTER}, /* earlier stop-after name */
		{"stop-at", "", POPT_ARG_STRING, nil, OPT_STOP_AT},
		{"rsh", "e", POPT_ARG_STRING, &o.shell_cmd, 0},
		{"rsync-path", "", POPT_ARG_STRING, &o.rsync_path, 0},
		{"temp-dir", "T", POPT_ARG_STRING, &o.tmpdir, 0},
		{"iconv", "", POPT_ARG_STRING, &o.iconv_opt, 0},
		{"no-iconv", "", POPT_ARG_NONE, nil, OPT_NO_ICONV},
		{"ipv4", "4", POPT_ARG_VAL, &o.default_af_hint, syscall.AF_INET},
		{"ipv6", "6", POPT_ARG_VAL, &o.default_af_hint, syscall.AF_INET6},
		{"8-bit-output", "8", POPT_ARG_VAL, &o.allow_8bit_chars, 1},
		{"no-8-bit-output", "", POPT_ARG_VAL, &o.allow_8bit_chars, 0},
		{"no-8", "", POPT_ARG_VAL, &o.allow_8bit_chars, 0},
		{"mkpath", "", POPT_ARG_VAL, &o.mkpath_dest_arg, 1},
		{"no-mkpath", "", POPT_ARG_VAL, &o.mkpath_dest_arg, 0},
		{"qsort", "", POPT_ARG_NONE, &o.use_qsort, 0},
		{"copy-as", "", POPT_ARG_STRING, &o.copy_as, 0},
		{"address", "", POPT_ARG_STRING, &o.bind_address, 0},
		{"port", "", POPT_ARG_INT, &o.rsync_port, 0},
		{"sockopts", "", POPT_ARG_STRING, &o.sockopts, 0},
		{"password-file", "", POPT_ARG_STRING, &o.password_file, 0},
		{"early-input", "", POPT_ARG_STRING, &o.early_input_file, 0},
		{"blocking-io", "", POPT_ARG_VAL, &o.blocking_io, 1},
		{"no-blocking-io", "", POPT_ARG_VAL, &o.blocking_io, 0},
		{"outbuf", "", POPT_ARG_STRING, &o.outbuf_mode, 0},
		{"remote-option", "M", POPT_ARG_STRING, nil, 'M'},
		{"protocol", "", POPT_ARG_INT, &o.protocol_version, 0},
		{"checksum-seed", "", POPT_ARG_INT, &o.checksum_seed, 0},
		{"server", "", POPT_ARG_NONE, nil, OPT_SERVER},
		{"sender", "", POPT_ARG_NONE, nil, OPT_SENDER},
		/* All the following options switch us into daemon-mode option-parsing. */
		{"config", "", POPT_ARG_STRING, nil, OPT_DAEMON},
		{"daemon", "", POPT_ARG_NONE, nil, OPT_DAEMON},
		{"dparam", "", POPT_ARG_STRING, nil, OPT_DAEMON},
		{"detach", "", POPT_ARG_NONE, nil, OPT_DAEMON},
		{"no-detach", "", POPT_ARG_NONE, nil, OPT_DAEMON},
	}
}

var errNotYetImplemented = errors.New("option not yet implemented in gokrazy/rsync")

// rsync/options.c:parse_arguments.
func ParseArguments(args []string, gokrazyTable bool) (*Context, error) {
	// NOTE: We do not implement support for refusing options per rsyncd.conf
	// here, as we have our own configuration file.

	version_opt_cnt := 0

	opts := NewOptions()
	table := opts.table()
	if gokrazyTable {
		// We need to make the --gokr.* flags known, otherwise the first parsing
		// attempt fails and the daemon mode parsing is never run.
		table = slices.Concat(opts.Gokrazy.table(), table)
	}
	pc := Context{
		Options: opts,
		table:   table,
		args:    args,
	}

	for {
		opt, err := pc.poptGetNextOpt()
		if err != nil {
			return nil, err
		}
		if opt == -1 {
			break // done
		}
		// Most options are handled by poptGetNextOpt, only special cases
		// are returned and handled here.
		switch opt {
		case 'V':
			version_opt_cnt++

		case OPT_SERVER:
			opts.am_server = 1

		case OPT_SENDER:
			if opts.am_server == 0 {
				return nil, fmt.Errorf("--sender only allowed with --server")
			}
			opts.am_sender = 1

		case OPT_DAEMON:
			// Parse the whole command-line using the daemon options table.
			table := opts.daemonTable()
			if gokrazyTable {
				table = slices.Concat(opts.Gokrazy.table(), table)
			}
			pc := Context{
				Options: opts,
				table:   table,
				args:    args,
			}

			for {
				opt, err := pc.poptGetNextOpt()
				if err != nil {
					return nil, err
				}
				if opt == -1 {
					break // done
				}
				// Most options are handled by poptGetNextOpt, only special cases
				// are returned and handled here.
				switch opt {
				case 'M':
					return nil, errNotYetImplemented
				case 'v':
					opts.verbose++
				default:
					return nil, fmt.Errorf("unhandled special case opt: %v", opt)
				}
			}

			opts.am_daemon = 1

			return &pc, nil

		case OPT_FILTER,
			OPT_EXCLUDE,
			OPT_INCLUDE,
			OPT_INCLUDE_FROM,
			OPT_EXCLUDE_FROM:
			return nil, errNotYetImplemented

		case 'a':
			if opts.recurse == 0 {
				opts.recurse = 1
			}
			opts.preserve_links = 1
			opts.preserve_perms = 1
			opts.preserve_mtimes = 1
			opts.preserve_gid = 1
			opts.preserve_uid = 1
			opts.preserve_devices = 1
			opts.preserve_specials = 1

		case 'D':
			opts.preserve_devices = 1
			opts.preserve_specials = 1

		case OPT_NO_D:
			opts.preserve_devices = 0
			opts.preserve_specials = 0

		case 'h':
			opts.human_readable++

		case 'H':
			opts.preserve_hard_links = 1

		case 'i':
			opts.itemize_changes++

		case 'U':
			opts.preserve_atimes++
			if opts.preserve_atimes > 1 {
				opts.open_noatime = 1
			}

		case 'v':
			opts.verbose++

		case 'y':
			return nil, errNotYetImplemented

		case 'q':
			opts.quiet++

		case 'x':
			opts.one_file_system++

		case 'F':
			return nil, errNotYetImplemented

		case 'P':
			opts.do_progress = 1
			opts.keep_partial = 1

		case 'z':
			opts.do_compression++

		case OPT_OLD_COMPRESS:
			opts.compress_choice = "zlib"

		case OPT_NEW_COMPRESS:
			opts.compress_choice = "zlibx"

		case OPT_NO_COMPRESS:
			opts.do_compression = 0
			opts.compress_choice = ""

		case OPT_OLD_ARGS:
			return nil, errNotYetImplemented

		case 'M': // --remote-option
			return nil, errNotYetImplemented

		case OPT_WRITE_BATCH,
			OPT_ONLY_WRITE_BATCH,
			OPT_READ_BATCH:
			return nil, errNotYetImplemented

		case OPT_BLOCK_SIZE:
			return nil, errNotYetImplemented

		case OPT_MAX_SIZE, // (needs parse_size_arg)
			OPT_MIN_SIZE,
			OPT_BWLIMIT:
			return nil, errNotYetImplemented

		case OPT_APPEND:
			return nil, errNotYetImplemented

		case OPT_LINK_DEST,
			OPT_COPY_DEST,
			OPT_COMPARE_DEST:
			return nil, errNotYetImplemented

		case OPT_CHMOD: // (needs parse_chmod):
			return nil, errNotYetImplemented

		case OPT_INFO:
			parseOutputWords(infoWords[:], opts.info[:], pc.poptGetOptArg(), USER_PRIORITY)

		case OPT_DEBUG:
			// TODO: plumb the debug level that make sense for our implementation
			slog.Info("TODO: set debug level", "to", pc.poptGetOptArg())

		case OPT_USERMAP,
			OPT_GROUPMAP,
			OPT_CHOWN:
			return nil, errNotYetImplemented

		case 'A':
			return nil, fmt.Errorf("ACLs are not supported by gokrazy/rsync")

		case 'X':
			opts.preserve_xattrs++

		case OPT_STOP_AFTER,
			OPT_STOP_AT,
			OPT_STDERR:
			return nil, errNotYetImplemented

		default:
			return nil, fmt.Errorf("unhandled special case opt: %v", opt)
		}
	}

	// rsync/options.c line 1973 and following set option defaults based on
	// other options

	opts.setOutputVerbosity(DEFAULT_PRIORITY)

	if opts.recurse != 0 {
		opts.xfer_dirs = 1
	}
	if opts.xfer_dirs < 0 {
		if opts.list_only != 0 {
			opts.xfer_dirs = 1
		} else {
			opts.xfer_dirs = 0
		}
	}

	if opts.relative_paths < 0 {
		if opts.files_from != "" {
			opts.relative_paths = 1
		} else {
			opts.relative_paths = 0
		}
	}

	if opts.relative_paths == 0 {
		opts.implied_dirs = 0
	}

	// NOTE: This simplification means that even if we ignore POPT_ARGFLAG_OR
	// and store ints without regards for bit sets, we get the same result.
	// Nevertheless, we support bit to be future-proof as new options are added.
	if opts.missing_args == 3 {
		// simplify if both options were specified
		opts.missing_args = 2
	}

	if opts.backup_suffix == "" && opts.backup_dir == "" {
		opts.backup_suffix = "~"
	}

	if opts.backup_dir != "" {
		opts.make_backups = 1 // --backup-dir implies --backup
	}

	if opts.do_progress != 0 /* && !opts.am_server */ {
		if opts.info[INFO_NAME] == 0 {
			opts.info[INFO_NAME] = 1
		}
	}

	if opts.info[INFO_NAME] >= 1 && opts.stdout_format == "" {
		opts.stdout_format = "%n%L"
	}

	return &pc, nil
}

// Package rsync implements the server side of the rsync protocol, the end
// an rsync client talks to over SSH ("rsync --server ..."). It speaks
// protocol versions 27 through 31, negotiates xxh64, md5 or md4 block
// checksums and zlib or zlibx compression, sends and receives deltas
// against existing files, and supports --checksum and the --delete modes.
//
// Files live in an FS, which the pico services back with their storage.
package rsync

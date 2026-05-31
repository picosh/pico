// Package rsync contains a native Go rsync implementation.
//
// The only component currently is gokr-rsyncd, a read-only rsync daemon
// sender-only Go implementation of rsyncd. rsync daemon is a custom
// (un-standardized) network protocol, running on port 873 by default.
package rsync

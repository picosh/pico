package nofollow

// Maybe resolves to unix.O_NOFOLLOW on unix systems,
// 0 on other platforms. TODO(go1.24): use os.Root.
const Maybe = 0

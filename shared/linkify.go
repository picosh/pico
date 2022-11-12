package shared

type Linkify interface {
	Create(fname string) string
}

type NullLinkify struct{}

func (n *NullLinkify) Create(s string) string {
	return ""
}

func NewNullLinkify() *NullLinkify {
	return &NullLinkify{}
}

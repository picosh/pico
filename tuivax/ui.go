package tuivax

import (
	"git.sr.ht/~rockorager/vaxis"
)

func NewTui(opts vaxis.Options) {
	vx, err := vaxis.New(opts)
	if err != nil {
		panic(err)
	}
	defer vx.Close()
	for ev := range vx.Events() {
		switch ev := ev.(type) {
		case vaxis.Key:
			switch ev.String() {
			case "Ctrl+c":
				return
			case "q":
				return
			case "Escape":
				return
			}
		}
		win := vx.Window()
		win.Clear()
		win.Print(vaxis.Segment{Text: "Hello, World!"})
		vx.Render()
	}
}

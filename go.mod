module git.sr.ht/~erock/pico

go 1.18

replace git.sr.ht/~erock/wish => /home/erock/pico/wish

require (
	git.sr.ht/~erock/wish v0.0.0-20220723165654-ad295e939d88
	go.uber.org/zap v1.21.0
)

require (
	github.com/lib/pq v1.10.6 // indirect
	go.uber.org/atomic v1.7.0 // indirect
	go.uber.org/multierr v1.6.0 // indirect
	golang.org/x/exp v0.0.0-20220613132600-b0d781184e0d // indirect
)

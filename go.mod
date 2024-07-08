module github.com/picosh/pico

go 1.22

// replace github.com/picosh/ptun => ../ptun

// replace github.com/picosh/send => ../send

// replace github.com/picosh/pobj => ../pobj

replace git.sr.ht/~delthas/senpai => github.com/picosh/senpai v0.0.0-20240503200611-af89e73973b0

replace github.com/gdamore/tcell/v2 => github.com/delthas/tcell/v2 v2.4.1-0.20230710100648-1489e78d90fb

require (
	git.sr.ht/~delthas/senpai v0.3.1-0.20240425235039-206be659439e
	github.com/alecthomas/chroma/v2 v2.14.0
	github.com/araddon/dateparse v0.0.0-20210429162001-6b43995a97de
	github.com/charmbracelet/bubbles v0.18.0
	github.com/charmbracelet/bubbletea v0.26.4
	github.com/charmbracelet/glamour v0.7.0
	github.com/charmbracelet/lipgloss v0.11.0
	github.com/charmbracelet/promwish v0.7.0
	github.com/charmbracelet/ssh v0.0.0-20240531200420-04041eb117c7
	github.com/charmbracelet/wish v1.4.0
	github.com/google/go-cmp v0.6.0
	github.com/gorilla/feeds v1.1.2
	github.com/lib/pq v1.10.9
	github.com/microcosm-cc/bluemonday v1.0.26
	github.com/minio/minio-go/v7 v7.0.70
	github.com/mmcdole/gofeed v1.3.0
	github.com/muesli/reflow v0.3.0
	github.com/neurosnap/go-exif-remove v0.0.0-20221010134343-50d1e3c35577
	github.com/picosh/pobj v0.0.0-20240708144544-9d0e2ca3d3dc
	github.com/picosh/ptun v0.0.0-20240529133708-fcf1376b935e
	github.com/picosh/send v0.0.0-20240706191319-a4ef3917a8bd
	github.com/sabhiram/go-gitignore v0.0.0-20210923224102-525f6e181f06
	github.com/sendgrid/sendgrid-go v3.14.0+incompatible
	github.com/simplesurance/go-ip-anonymizer v0.0.0-20200429124537-35a880f8e87d
	github.com/x-way/crawlerdetect v0.2.21
	github.com/yuin/goldmark v1.7.1
	github.com/yuin/goldmark-highlighting/v2 v2.0.0-20230729083705-37449abec8cc
	github.com/yuin/goldmark-meta v1.1.0
	go.abhg.dev/goldmark/anchor v0.1.1
	go.abhg.dev/goldmark/hashtag v0.3.1
	go.abhg.dev/goldmark/toc v0.10.0
	golang.org/x/crypto v0.23.0
	gopkg.in/yaml.v2 v2.4.0
)

require (
	git.sr.ht/~emersion/go-scfg v0.0.0-20240128091534-2ae16e782082 // indirect
	github.com/DavidGamba/go-getoptions v0.30.0 // indirect
	github.com/PuerkitoBio/goquery v1.9.2 // indirect
	github.com/andybalholm/cascadia v1.3.2 // indirect
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/antoniomika/go-rsync-receiver v0.0.0-20231110145728-c94949e1ab7d // indirect
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/charmbracelet/keygen v0.5.0 // indirect
	github.com/charmbracelet/log v0.4.0 // indirect
	github.com/charmbracelet/x/ansi v0.1.2 // indirect
	github.com/charmbracelet/x/errors v0.0.0-20240603171354-f37319acd141 // indirect
	github.com/charmbracelet/x/exp/term v0.0.0-20240603171354-f37319acd141 // indirect
	github.com/charmbracelet/x/input v0.1.2 // indirect
	github.com/charmbracelet/x/term v0.1.1 // indirect
	github.com/charmbracelet/x/termios v0.1.0 // indirect
	github.com/charmbracelet/x/windows v0.1.2 // indirect
	github.com/creack/pty v1.1.21 // indirect
	github.com/delthas/go-libnp v0.0.0-20221222161248-0e45ece1f878 // indirect
	github.com/delthas/go-localeinfo v0.0.0-20221116001557-686a1e185118 // indirect
	github.com/dlclark/regexp2 v1.11.0 // indirect
	github.com/dsoprea/go-exif v0.0.0-20230826092837-6579e82b732d // indirect
	github.com/dsoprea/go-exif/v2 v2.0.0-20230826092837-6579e82b732d // indirect
	github.com/dsoprea/go-iptc v0.0.0-20200610044640-bc9ca208b413 // indirect
	github.com/dsoprea/go-logging v0.0.0-20200710184922-b02d349568dd // indirect
	github.com/dsoprea/go-photoshop-info-format v0.0.0-20200610045659-121dd752914d // indirect
	github.com/dsoprea/go-png-image-structure v0.0.0-20210512210324-29b889a6093d // indirect
	github.com/dsoprea/go-utility v0.0.0-20221003172846-a3e1774ef349 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/erikgeiser/coninput v0.0.0-20211004153227-1c3628e74d0f // indirect
	github.com/forPelevin/gomoji v1.2.0 // indirect
	github.com/gdamore/encoding v1.0.1 // indirect
	github.com/gdamore/tcell/v2 v2.7.4 // indirect
	github.com/go-errors/errors v1.5.1 // indirect
	github.com/go-logfmt/logfmt v0.6.0 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/go-xmlfmt/xmlfmt v1.1.2 // indirect
	github.com/goccy/go-json v0.10.3 // indirect
	github.com/godbus/dbus/v5 v5.1.0 // indirect
	github.com/golang-jwt/jwt/v4 v4.5.0 // indirect
	github.com/golang/geo v0.0.0-20230421003525-6adc56603217 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.17.8 // indirect
	github.com/klauspost/cpuid/v2 v2.2.7 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.2.0 // indirect
	github.com/lufia/plan9stats v0.0.0-20240513124658-fba389f38bae // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-localereader v0.0.1 // indirect
	github.com/mattn/go-runewidth v0.0.15 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.4 // indirect
	github.com/minio/madmin-go/v3 v3.0.54 // indirect
	github.com/minio/md5-simd v1.1.2 // indirect
	github.com/mmcdole/goxpp v1.1.1 // indirect
	github.com/mmcloughlin/md4 v0.1.2 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/muesli/ansi v0.0.0-20230316100256-276c6243b2f6 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/muesli/termenv v0.15.2 // indirect
	github.com/neurosnap/go-jpeg-image-structure v0.0.0-20221010133817-70b1c1ff679e // indirect
	github.com/olekukonko/tablewriter v0.0.5 // indirect
	github.com/philhofer/fwd v1.1.2 // indirect
	github.com/pkg/sftp v1.13.6 // indirect
	github.com/power-devops/perfstat v0.0.0-20240221224432-82ca36839d55 // indirect
	github.com/prometheus/client_golang v1.19.1 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/common v0.54.0 // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	github.com/prometheus/prom2json v1.3.3 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/rogpeppe/go-internal v1.11.0 // indirect
	github.com/rs/xid v1.5.0 // indirect
	github.com/safchain/ethtool v0.3.0 // indirect
	github.com/secure-io/sio-go v0.3.1 // indirect
	github.com/sendgrid/rest v2.6.9+incompatible // indirect
	github.com/shirou/gopsutil/v3 v3.24.5 // indirect
	github.com/shoenig/go-m1cpu v0.1.6 // indirect
	github.com/tinylib/msgp v1.1.9 // indirect
	github.com/tklauser/go-sysconf v0.3.14 // indirect
	github.com/tklauser/numcpus v0.8.0 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	github.com/yuin/goldmark-emoji v1.0.2 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	golang.org/x/exp v0.0.0-20240531132922-fd00a4e0eefc // indirect
	golang.org/x/net v0.25.0 // indirect
	golang.org/x/sync v0.7.0 // indirect
	golang.org/x/sys v0.20.0 // indirect
	golang.org/x/term v0.20.0 // indirect
	golang.org/x/text v0.15.0 // indirect
	golang.org/x/time v0.5.0 // indirect
	google.golang.org/protobuf v1.34.1 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
	mvdan.cc/xurls/v2 v2.5.0 // indirect
)

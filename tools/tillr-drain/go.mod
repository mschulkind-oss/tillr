module github.com/mschulkind-oss/tillr-drain

go 1.26.0

require (
	github.com/mschulkind-oss/tillr v0.0.0
	github.com/mschulkind-oss/tillr-item v0.0.0
	github.com/mschulkind-oss/tillr-run v0.0.0
	golang.org/x/sys v0.49.0
	modernc.org/sqlite v1.61.0
)

require (
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/pelletier/go-toml/v2 v2.2.3 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	modernc.org/libc v1.79.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

replace github.com/mschulkind-oss/tillr => ../tillr

replace github.com/mschulkind-oss/tillr-item => ../tillr-item

replace github.com/mschulkind-oss/tillr-run => ../tillr-run

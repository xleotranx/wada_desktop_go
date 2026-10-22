module wada-desktop-go

go 1.27.2

tool github.com/egoist/mygo/cmd/mygo

require (
	github.com/egoist/mygo v0.5.5
	github.com/oneo-me/onevra/source v0.2026.13
	github.com/xleotranx/wada-kit v0.0.0
)

// The kit is developed beside the app; once it moves to its own repository,
// this goes and the require names a tagged version.
replace github.com/xleotranx/wada-kit => ./wada-kit

require (
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	golang.org/x/image v0.47.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

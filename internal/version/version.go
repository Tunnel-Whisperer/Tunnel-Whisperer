package version

// Version is set at build time via -ldflags:
//
//	go build -ldflags "-X github.com/tunnelwhisperer/tw/internal/version.Version=v1.2.3"
//
// When not set (local dev builds), defaults to "dev".
var Version = "dev"

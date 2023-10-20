package buildinfo

import (
	"fmt"
	"runtime/debug"
)

var Major = ""
var Minor = ""
var Patch = ""

func init() {
	if Major == "" {
		Major = "0"
	}
	if Minor == "" {
		Minor = "0"
	}
	if Patch == "" {
		Patch = "1"
	}
}

func getCommitHash() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				return s.Value
			}
		}
	}
	return ""
}

func isDirty() bool {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.modified" {
				return s.Value == "true"
			}
		}
	}
	return false
}

func GetVersion() string {
	extra := ""
	hash := getCommitHash()
	if len(hash) >= 7 {
		extra += "-" + hash[0:7]
	}
	if isDirty() {
		extra += "-dirty"
	}
	return fmt.Sprintf("%s.%s.%s%s", Major, Minor, Patch, extra)
}

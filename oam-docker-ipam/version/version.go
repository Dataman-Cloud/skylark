package version

import (
	"io"
	"runtime"
	"text/template"
)

var (
	version   string // set by build LD_FLAGS
	gitCommit string // set by build LD_FLAGS
	buildAt   string // set by build LD_FLAGS
)

var versionTemplate = ` Version:      {{.Version}}
 Git commit:   {{.GitCommit}}
 Go version:   {{.GoVersion}}
 Built:        {{.BuildTime}}
 OS/Arch:      {{.Os}}/{{.Arch}}
`

// Version is exported
type Version struct {
	Version   string `json:"version"`
	GoVersion string `json:"go_version"`
	GitCommit string `json:"git_commit"`
	BuildTime string `json:"build_time"`
	Os        string `json:"os"`
	Arch      string `json:"arch"`
}

// WriteTo is exported
func (v Version) WriteTo(w io.Writer) error {
	tmpl, _ := template.New("version").Parse(versionTemplate)
	return tmpl.Execute(w, v)
}

// Version is exported
func GetVersion() Version {
	return Version{
		Version:   version,
		GitCommit: gitCommit,
		BuildTime: buildAt,
		GoVersion: runtime.Version(),
		Os:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}
